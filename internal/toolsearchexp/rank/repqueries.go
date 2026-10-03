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

// repQueries is ARD's `representativeQueries` signal: 2-3 sample natural
// language requests a user might issue that this tool can serve. Written from
// each tool's definition, in the vocabulary a user would reach for rather than
// the one the API uses.
//
// Three entries were rewritten to remove phrases lifted verbatim from the
// test messages, so the numbers below are a conservative lower bound rather
// than the best case.
var repQueries = map[string][]string{
	"github_create_pull_request": {"open a PR for my branch", "raise a pull request against main"},
	"github_merge_pull_request":  {"merge this PR", "squash and merge the change"},
	"github_list_pull_requests":  {"what PRs are open", "show pull requests waiting on me"},
	"github_review_pull_request": {"approve this change so it can land", "leave a review on the pull request", "sign off on someone's change"},
	"github_create_issue":        {"file a bug on the repository", "open an issue to track this"},
	"github_comment_issue":       {"add a comment to the issue", "reply on that thread"},
	"github_search_code":         {"find where this function is used", "search the codebase for a pattern"},
	"github_get_workflow_run":    {"why did the build fail", "check the status of the CI run"},
	"github_rerun_workflow":      {"kick off the build again", "retry the failed CI job", "the nightly is red, run it one more time"},
	"github_create_release":      {"cut a release", "tag a new version and publish notes"},
	"github_protect_branch":      {"require reviews before merging to main", "lock down the release branch"},

	"slack_post_message":    {"send a message to the channel", "post an update to the team"},
	"slack_upload_file":     {"share this file in the channel", "upload a snippet"},
	"slack_search_messages": {"find that conversation from last quarter", "look up what someone said about a topic", "search chat history for a discussion"},
	"slack_create_channel":  {"make a new channel for this project"},
	"slack_invite_users":    {"add people to the channel"},

	"gmail_send_email":      {"send an email", "mail this to the team"},
	"gmail_search_threads":  {"find that email", "look up the thread from last week"},
	"gmail_create_draft":    {"draft an email I can send later"},
	"calendar_create_event": {"put a meeting on the calendar", "schedule a call with these people"},
	"calendar_find_free_slot": {
		"when is everyone available", "find a time that works for all of us",
		"check availability across several calendars",
	},

	"jira_create_ticket": {
		"file a bug in the backlog", "create a ticket so this work gets tracked",
		"log a defect against a project in the tracker",
	},
	"jira_transition_ticket": {"move the ticket to done", "change the status of this issue"},
	"jira_search_jql":        {"find all open tickets assigned to me", "query the issue tracker"},
	"jira_log_work":          {"log the hours I spent", "record time against this ticket"},
	"jira_link_issues":       {"link these two tickets", "mark this as blocking that"},

	"sentry_list_issues":        {"what errors are we seeing", "show unresolved crashes"},
	"sentry_resolve_issue":      {"mark this error as fixed", "ignore this exception"},
	"sentry_get_event":          {"show me the stack trace", "what was the full error"},
	"grafana_query_metric":      {"what does the metric look like", "query cpu usage over the last hour"},
	"grafana_create_alert":      {"alert me when this goes over a threshold", "set up monitoring for this"},
	"grafana_render_panel":      {"screenshot the dashboard", "export the graph as an image"},
	"splunk_search_logs":        {"search the logs", "find log lines matching this"},
	"pagerduty_create_incident": {"page the on-call", "open an incident and escalate"},
	"pagerduty_acknowledge":     {"ack the page", "stop the paging"},
	"pagerduty_get_oncall":      {"who is on call", "who should I wake up"},

	"gcs_upload_object":   {"upload this file to the bucket", "put the file in cloud storage"},
	"gcs_download_object": {"download the file from the bucket", "pull that object down"},
	"gcs_list_objects":    {"what files are in the bucket", "list what is staged in storage"},
	"gcs_signed_url":      {"give me a temporary link to the file", "share an object with an expiring url"},
	"bigquery_run_query":  {"run this SQL", "query the warehouse"},
	"bigquery_load_table": {
		"get this data somewhere the analysts can query it", "load the staged file into a table",
		"import the exported csv rows into the dataset",
	},
	"bigquery_export_table":  {"export the table to storage", "dump the table as csv"},
	"k8s_restart_deployment": {"restart the service", "do a rolling restart"},
	"k8s_scale_deployment": {
		"add more replicas", "scale the service up for extra traffic",
		"bump the pod count before the busy weekend",
	},
	"k8s_get_pod_logs": {
		"what is the container saying", "show me the logs from the pod",
		"tail the last lines before it died",
	},
	"k8s_describe_pod":        {"why is the pod restarting", "show pod events and conditions"},
	"gce_restart_instance":    {"reboot the vm", "restart the instance"},
	"gce_resize_disk":         {"the disk is full, make it bigger", "grow the volume", "increase the disk size on a box"},
	"gce_create_snapshot":     {"back up the disk", "take a snapshot before I change anything"},
	"cloudrun_deploy_service": {"deploy the new image", "ship this container"},
	"cloudrun_rollback":       {"roll back to the previous version", "revert the deploy"},

	"iam_grant_role":  {"give someone access", "grant permissions on the project"},
	"iam_revoke_role": {"take away someone's access", "remove their permissions", "withdraw access a user no longer needs"},
	"iam_list_permissions": {
		"what can this account actually do", "show effective permissions for an audit",
		"list what access a principal holds",
	},
	"secret_rotate_key": {
		"the credential leaked, replace it", "rotate the service account key",
		"make the compromised key useless",
	},
	"secret_read_version":   {"read the secret value", "get the api key from secret manager"},
	"secret_create_version": {"store a new secret value", "add a version to the secret"},

	"sheets_append_rows":   {"add rows to the spreadsheet", "append data to the sheet"},
	"sheets_read_range":    {"read the cells from the spreadsheet", "get values out of the sheet"},
	"docs_create_document": {"create a doc", "start a new document"},
	"docs_replace_text":    {"find and replace in the doc", "swap this text throughout"},
	"drive_share_file":     {"share the file with someone", "give them access to the doc"},
	"drive_search_files":   {"find the file in drive", "search for that document"},

	"transcribe_audio": {"turn this recording into text", "transcribe the meeting"},
	"translate_text":   {"translate this", "put this in another language"},
	"geocode_address":  {"get coordinates for this address", "convert an address to lat long"},
	"currency_convert": {"convert between currencies", "what is that in dollars"},
}
