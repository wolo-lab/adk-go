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

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestDiscoveryThroughADKRunner(t *testing.T) {
	for _, skillFirst := range []bool{false, true} {
		var out bytes.Buffer
		llm := &scriptedModel{skillFirst: skillFirst}
		if err := run(t.Context(), llm, "Multiply 6 by 7.", &out); err != nil {
			t.Fatal(err)
		}
		if llm.step != 5 || !strings.Contains(out.String(), "execution verified") {
			t.Fatalf("incomplete discovery/execution: steps=%d output=%s", llm.step, out.String())
		}
	}
}
