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

// Case is one realistic user message paired with the tool that answers it.
// The messages are written the way people actually type: context first,
// the ask buried somewhere, and plenty of vocabulary that belongs to other
// tools in the catalog.
type Case struct {
	Query string
	Want  string
}

var cases = []Case{
	{
		Query: `Morning. So we had that incident last night around 2am where the checkout
service started throwing 500s and the on-call got paged. I've been going through the
postmortem notes and it looks like the root cause was the payment provider timing out,
not anything on our side, but the alert was still firing this morning when I checked.
Anyway the immediate thing I need is to bump the number of replicas for the checkout
deployment in the prod namespace from 3 to 8, because we're heading into the sale
weekend and Marek thinks we'll get roughly 4x the usual traffic. Can you sort that out
before standup?`,
		Want: "k8s_scale_deployment",
	},
	{
		Query: `Hey, quick one. The finance team has been chasing me for two weeks about the
Q3 numbers and I finally have them. I've got a local CSV with about 40k rows of
transaction data that I exported from the old system. It needs to end up somewhere the
analysts can actually query it - they've been complaining that they can't do anything
with spreadsheets at that size. I already created the dataset and the table with the
right schema yesterday. So what's left is getting the data from the bucket where I
staged it into that table. The file is already sitting in cloud storage.`,
		Want: "bigquery_load_table",
	},
	{
		Query: `I'm trying to wrap up the release for tomorrow but I'm blocked. There's a
change from Ania that's been sitting for three days - it fixes the null pointer thing in
the session handler that keeps showing up in our error tracker. I've read through it, the
tests look reasonable, CI is green, and honestly it's a two-line change. I already left a
couple of comments earlier in the week and she addressed both of them. At this point I
just want to approve it so she can land it and I can cut the release branch. Repo is
platform/core, it's number 4471.`,
		Want: "github_review_pull_request",
	},
	{
		Query: `So there's a bit of a mess with permissions. We onboarded three contractors
last month and someone - I think it was during the rush before the demo - gave the whole
group write access to the production project instead of just staging. Security flagged it
in their quarterly sweep. Two of the contractors have already rolled off the project as
of last Friday. I need to take away the editor binding that dana@contractor.example has on
the prod project. Just hers for now, I'll deal with the other two once I confirm with
their manager that they're actually done.`,
		Want: "iam_revoke_role",
	},
	{
		Query: `Ugh. I think we leaked something. One of the interns pushed a commit to a
public repo that had a service account JSON in it - it's been up for maybe six hours
before our scanner caught it. We've already force-pushed to remove it from history but
obviously that's not enough, the thing is compromised. The account is
build-runner@ourproject.iam.gserviceaccount.com and it has deploy permissions on
basically everything, which is its own problem we should talk about later. Right now the
urgent thing is making that credential useless. New one needs to work before the 4pm
deploy window.`,
		Want: "secret_rotate_key",
	},
	{
		Query: `Can you help me find something? Last quarter, maybe October, there was a
long conversation in one of the engineering channels about whether we should move off
the shared Postgres instance. I remember Tomek made a good argument about connection
pooling and someone posted a graph. I've been looking for it for twenty minutes because
I want to link it in the design doc I'm writing, but I can't remember which channel it
was in - could have been platform, could have been backend-general. Search term would be
something like connection pooling or pgbouncer.`,
		Want: "slack_search_messages",
	},
	{
		Query: `We're doing the quarterly access review and I have to produce evidence for
the auditors. What they want, specifically, is a list showing exactly what each service
account can do on the data warehouse project - not the role names, they've seen those,
but the actual effective permissions that come out of the role bindings and any
inherited policy from the folder. Last time I did this by hand from the console and it
took most of a day and they still sent it back. Start with the etl-runner principal,
that's the one they asked about first.`,
		Want: "iam_list_permissions",
	},
	{
		Query: `The nightly build has been red since Tuesday and nobody's looking at it
because everyone's heads-down on the migration. I finally dug in this morning. It's not
actually a code problem - the flaky integration test that talks to the emulator times out
maybe one run in four, and Tuesday it just happened to hit three times in a row. The
underlying fix is a bigger conversation. For now I want to kick off that build again and
see if it goes green, because if it does I can unblock the two people waiting on the
artifact. It's the nightly workflow in the infra repo, run id 88214031.`,
		Want: "github_rerun_workflow",
	},
	{
		Query: `Planning problem. We need to get the five of us plus someone from legal in
a room - or a call, doesn't matter - to go through the data retention changes before the
policy deadline at the end of the month. I've tried doing this over chat twice and it
just doesn't work, people keep proposing times that clash. Everyone's calendars are a
disaster next week. I need ninety minutes, ideally in the afternoon European time so
that Sofia in Boston can make it too, and it has to be before the 28th. Just tell me when
everyone is actually available and I'll send the invite myself.`,
		Want: "calendar_find_free_slot",
	},
	{
		Query: `Right, so the disk situation. The analytics box has been running at 94%
full for a couple of weeks and I've been ignoring it, and now the batch job that writes
the intermediate parquet files is failing about half the time because it runs out of
space mid-write. I've already cleaned up what I can - old logs, the stale checkpoint
directory, that sort of thing - and it bought me maybe two days. The real answer is it
just needs to be bigger. It's a 500GB persistent disk right now, I'd like to take it to
1.5TB. Instance is analytics-worker-3 in europe-west1-b.`,
		Want: "gce_resize_disk",
	},
	{
		Query: `Customer escalation, and it's getting ugly. Nordwind have been on us since
Thursday about the export feature silently dropping rows. Their account manager has
already gone over my head once. I've reproduced it locally and I know roughly where it
is - the pagination cursor resets when a batch comes back empty - but I'm not going to
get to a fix today because I'm covering on-call. This needs to exist as a tracked item
in the backlog so it doesn't evaporate, with enough detail that whoever picks it up
tomorrow isn't starting from zero. Project is PLAT, should probably be a bug at high
priority.`,
		Want: "jira_create_ticket",
	},
	{
		Query: `Something odd is happening with the image processing workers and I can't
tell if it's real. The dashboard shows memory climbing steadily over about six hours and
then dropping off a cliff, which to me looks like they're getting OOM killed and
restarted, but the restart count on the dashboard isn't moving which doesn't fit. Before
I go and file anything or wake anyone up I want to look at what the container itself was
saying in the last few minutes before one of those drops. Pod is
image-worker-7d9f8-qx2lm, namespace media. Last couple hundred lines would be plenty.`,
		Want: "k8s_get_pod_logs",
	},
}
