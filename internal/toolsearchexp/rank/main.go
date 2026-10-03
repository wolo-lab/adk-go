// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Command rank measures how well different scoring strategies pick the right
// tool out of a catalog when the query is a long, noisy user message.
//
// The question it answers: can a pre-search running before the first model
// call narrow a large catalog down to a handful of candidates without losing
// the tool the user actually needs?
package main

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
)

// --- tokenization ---

var stop = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`a an the and or but if then than that this these those
		i me my we our you your he she it they them there here is are was were be been being
		do does did doing have has had having will would can could should shall may might must
		of in on at to for with from by about into over after before between out up down off
		no not so just now already still also very really quite much many some any all both
		each few more most other such only own same too as because while when where which who
		whom what how why get got go going went one two three four five got need needs needed
		want wants wanted like likes thing things stuff sort kind bit lot right okay ok yeah
		anyway actually basically honestly probably maybe im ive its dont doesnt cant wont
		day days week weeks month months morning afternoon night today tomorrow yesterday
		time times back down out through again against once`) {
		stop[w] = true
	}
}

// tokenize lowercases, splits on non-alphanumerics, drops stopwords and very
// short tokens, and also emits the pieces of snake_case identifiers.
func tokenize(s string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		t := strings.ToLower(cur.String())
		cur.Reset()
		if len(t) < 3 || stop[t] {
			return
		}
		out = append(out, t)
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			cur.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return out
}

// stem is a crude suffix trimmer: enough to tie "restarting" to "restart"
// and "permissions" to "permission" without pulling in a dependency.
func stem(t string) string {
	for _, suf := range []string{"ing", "ed", "es", "s"} {
		if len(t) > len(suf)+3 && strings.HasSuffix(t, suf) {
			return strings.TrimSuffix(t, suf)
		}
	}
	return t
}

func terms(s string) []string {
	toks := tokenize(s)
	out := make([]string, 0, len(toks))
	for _, t := range toks {
		out = append(out, stem(t))
	}
	return out
}

// --- index ---

type index struct {
	docs    [][]string         // per tool: name terms + desc terms + arg terms
	nameTok []map[string]bool  // per tool: terms coming from the name
	idf     map[string]float64 // over the catalog
	avgLen  float64
}

func buildIndex(c []Tool, withRepQueries bool) *index {
	ix := &index{idf: map[string]float64{}}
	df := map[string]int{}
	total := 0
	for _, t := range c {
		nt := terms(t.Name)
		doc := slices.Clone(nt)
		doc = append(doc, terms(t.Desc)...)
		doc = append(doc, terms(t.Args)...)
		if withRepQueries {
			for _, q := range repQueries[t.Name] {
				doc = append(doc, terms(q)...)
			}
		}
		ix.docs = append(ix.docs, doc)

		nm := map[string]bool{}
		for _, x := range nt {
			nm[x] = true
		}
		ix.nameTok = append(ix.nameTok, nm)

		seen := map[string]bool{}
		for _, x := range doc {
			if !seen[x] {
				seen[x] = true
				df[x]++
			}
		}
		total += len(doc)
	}
	n := float64(len(c))
	for t, d := range df {
		ix.idf[t] = math.Log(1 + (n-float64(d)+0.5)/(float64(d)+0.5))
	}
	ix.avgLen = float64(total) / n
	return ix
}

// --- rankers ---

type ranker struct {
	name  string
	score func(ix *index, i int, q []string, qset map[string]bool) float64
}

// substring is the naive matcher: does any tool term appear in the message.
// This is what the first prototype did.
func substringScore(ix *index, i int, q []string, qset map[string]bool) float64 {
	for _, t := range ix.docs[i] {
		if qset[t] {
			return 1
		}
	}
	return 0
}

// bm25 is textbook BM25 with the user message as the query. This is the
// direction the design doc implies, and the direction BM25 was designed for
// is the opposite one: short query, long documents.
func bm25Score(ix *index, i int, q []string, qset map[string]bool) float64 {
	const k1, b = 1.2, 0.75
	tf := map[string]int{}
	for _, t := range ix.docs[i] {
		tf[t]++
	}
	dl := float64(len(ix.docs[i]))
	var s float64
	for _, qt := range q {
		f := float64(tf[qt])
		if f == 0 {
			continue
		}
		s += ix.idf[qt] * (f * (k1 + 1)) / (f + k1*(1-b+b*dl/ix.avgLen))
	}
	return s
}

// coverage inverts the direction: for each term the TOOL contributes, ask
// whether the message contains it, weighted by how distinctive that term is.
// Normalizing by the tool's own weight stops verbose tools from winning.
func coverageScore(ix *index, i int, q []string, qset map[string]bool) float64 {
	var hit, total float64
	seen := map[string]bool{}
	for _, t := range ix.docs[i] {
		if seen[t] {
			continue
		}
		seen[t] = true
		w := ix.idf[t]
		total += w
		if qset[t] {
			hit += w
		}
	}
	if total == 0 {
		return 0
	}
	return hit / total
}

// coverageName is coverage with terms from the tool NAME weighted higher,
// on the theory that a tool's name carries its intent more reliably than
// its prose description.
func coverageNameScore(ix *index, i int, q []string, qset map[string]bool) float64 {
	const nameBoost = 3.0
	var hit, total float64
	seen := map[string]bool{}
	for _, t := range ix.docs[i] {
		if seen[t] {
			continue
		}
		seen[t] = true
		w := ix.idf[t]
		if ix.nameTok[i][t] {
			w *= nameBoost
		}
		total += w
		if qset[t] {
			hit += w
		}
	}
	if total == 0 {
		return 0
	}
	return hit / total
}

// hybrid multiplies coverage by a damped BM25, so a tool must both be
// well-covered by the message and share distinctive vocabulary with it.
func hybridScore(ix *index, i int, q []string, qset map[string]bool) float64 {
	return coverageNameScore(ix, i, q, qset) * math.Log(1+bm25Score(ix, i, q, qset))
}

// --- evaluation ---

func rankAll(ix *index, r ranker, query string) []int {
	q := terms(query)
	qset := map[string]bool{}
	for _, t := range q {
		qset[t] = true
	}
	type sc struct {
		i int
		s float64
	}
	all := make([]sc, len(ix.docs))
	for i := range ix.docs {
		all[i] = sc{i, r.score(ix, i, q, qset)}
	}
	sort.SliceStable(all, func(a, b int) bool { return all[a].s > all[b].s })
	out := make([]int, len(all))
	for i, x := range all {
		out[i] = x.i
	}
	return out
}

func main() {
	rankers := []ranker{
		{"substring (pierwszy prototyp)", substringScore},
		{"BM25 (zapytanie = wiadomość)", bm25Score},
		{"coverage (odwrócony kierunek)", coverageScore},
		{"coverage + boost na nazwę", coverageNameScore},
		{"hybrid (coverage x BM25)", hybridScore},
	}
	ks := []int{1, 5, 10, 20}

	fmt.Printf("Katalog: %d narzędzi. Zapytań: %d. Średnia długość wiadomości: %d słów.\n",
		len(catalog), len(cases), avgWords())

	for _, variant := range []struct {
		label string
		rep   bool
	}{
		{"BEZ representativeQueries (indeks = nazwa + opis + argumenty)", false},
		{"Z  representativeQueries (ARD: + 2-3 przykładowe zapytania)", true},
	} {
		ix := buildIndex(catalog, variant.rep)
		fmt.Printf("\n=== %s ===\n", variant.label)
		report(ix, rankers, ks)
	}

	// Per-case detail, best ranker, with representativeQueries.
	ixRep := buildIndex(catalog, true)
	ixBase := buildIndex(catalog, false)
	best := rankers[2]
	fmt.Printf("\nPrzesunięcia pozycji dla %q:\n\n", best.name)
	for _, c := range cases {
		before := posOf(ixBase, best, c)
		after := posOf(ixRep, best, c)
		delta := "="
		if after < before {
			delta = fmt.Sprintf("-%d", before-after)
		} else if after > before {
			delta = fmt.Sprintf("+%d", after-before)
		}
		fmt.Printf("  %-28s  %2d -> %2d  (%s)\n", c.Want, before, after, delta)
	}
}

func posOf(ix *index, r ranker, c Case) int {
	for i, toolIdx := range rankAll(ix, r, c.Query) {
		if catalog[toolIdx].Name == c.Want {
			return i + 1
		}
	}
	return -1
}

func report(ix *index, rankers []ranker, ks []int) {
	fmt.Printf("%-32s", "ranker")
	for _, k := range ks {
		fmt.Printf("  recall@%-3d", k)
	}
	fmt.Println("   mediana pozycji")
	fmt.Println(strings.Repeat("-", 90))

	for _, r := range rankers {
		hits := map[int]int{}
		var positions []int
		for _, c := range cases {
			order := rankAll(ix, r, c.Query)
			pos := -1
			for rankIdx, toolIdx := range order {
				if catalog[toolIdx].Name == c.Want {
					pos = rankIdx + 1
					break
				}
			}
			positions = append(positions, pos)
			for _, k := range ks {
				if pos >= 1 && pos <= k {
					hits[k]++
				}
			}
		}
		fmt.Printf("%-32s", r.name)
		for _, k := range ks {
			fmt.Printf("  %6.0f%%   ", 100*float64(hits[k])/float64(len(cases)))
		}
		slices.Sort(positions)
		fmt.Printf("   %d\n", positions[len(positions)/2])
	}
}

func avgWords() int {
	n := 0
	for _, c := range cases {
		n += len(strings.Fields(c.Query))
	}
	return n / len(cases)
}
