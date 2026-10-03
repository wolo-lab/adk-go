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

// Tool is one catalog entry. Args carries parameter names and descriptions,
// which the design doc wants indexed alongside the name and description.
type Tool struct {
	Name string
	Desc string
	Args string
}

// catalog is a plausible aggregation of several MCP servers.
var catalog = []Tool{
	// --- source control ---
	{"github_create_pull_request", "Open a pull request from a head branch into a base branch.", "owner repo head base title body draft"},
	{"github_merge_pull_request", "Merge an open pull request using merge, squash or rebase.", "owner repo pull_number merge_method commit_title"},
	{"github_list_pull_requests", "List pull requests filtered by state, author or label.", "owner repo state author labels sort"},
	{"github_review_pull_request", "Submit an approving, commenting or change-requesting review.", "owner repo pull_number event body comments"},
	{"github_create_issue", "File a new issue with a title, body and labels.", "owner repo title body labels assignees"},
	{"github_comment_issue", "Add a comment to an existing issue or pull request.", "owner repo issue_number body"},
	{"github_search_code", "Search code across repositories by expression and language.", "query language repo path limit"},
	{"github_get_workflow_run", "Fetch status, conclusion and logs of an Actions workflow run.", "owner repo run_id include_logs"},
	{"github_rerun_workflow", "Re-run a failed GitHub Actions workflow run.", "owner repo run_id only_failed_jobs"},
	{"github_create_release", "Cut a release with a tag, notes and attached assets.", "owner repo tag_name name body prerelease"},
	{"github_protect_branch", "Set branch protection rules requiring reviews and status checks.", "owner repo branch required_reviews required_checks"},

	// --- chat and mail ---
	{"slack_post_message", "Post a message to a Slack channel or thread.", "channel text thread_ts blocks"},
	{"slack_upload_file", "Upload a file or snippet to a Slack channel.", "channels file filename initial_comment"},
	{"slack_search_messages", "Search Slack history for messages matching a query.", "query channel from before after count"},
	{"slack_create_channel", "Create a public or private Slack channel.", "name is_private topic purpose"},
	{"slack_invite_users", "Invite users to an existing Slack channel.", "channel users"},
	{"gmail_send_email", "Send an email message to one or more recipients.", "to cc bcc subject body attachments"},
	{"gmail_search_threads", "Search mail threads by sender, subject, label or date.", "query label from subject after before"},
	{"gmail_create_draft", "Create a draft email without sending it.", "to subject body thread_id"},
	{"calendar_create_event", "Create a calendar event with attendees and a time range.", "summary start end attendees location description"},
	{"calendar_find_free_slot", "Find a mutually free time window across several calendars.", "attendees duration_minutes earliest latest timezone"},

	// --- issue tracking ---
	{"jira_create_ticket", "Create a Jira ticket in a project with a type and priority.", "project summary description issue_type priority assignee"},
	{"jira_transition_ticket", "Move a Jira ticket to another workflow status.", "issue_key transition resolution comment"},
	{"jira_search_jql", "Run a JQL query and return matching Jira issues.", "jql fields max_results start_at"},
	{"jira_log_work", "Log time spent against a Jira ticket.", "issue_key time_spent started comment"},
	{"jira_link_issues", "Create a link between two Jira issues.", "inward_issue outward_issue link_type"},

	// --- observability ---
	{"sentry_list_issues", "List unresolved Sentry issues for a project, newest first.", "organization project query statsPeriod environment"},
	{"sentry_resolve_issue", "Mark a Sentry issue as resolved or ignored.", "issue_id status ignore_duration"},
	{"sentry_get_event", "Fetch the full stack trace and breadcrumbs for one event.", "issue_id event_id"},
	{"grafana_query_metric", "Run a PromQL query against a Grafana datasource.", "datasource query start end step"},
	{"grafana_create_alert", "Create an alert rule with a threshold and notification channel.", "name query threshold for_duration channel"},
	{"grafana_render_panel", "Render a dashboard panel to a PNG image.", "dashboard_uid panel_id width height from to"},
	{"splunk_search_logs", "Run a Splunk search over indexed log events.", "search earliest latest index max_count"},
	{"pagerduty_create_incident", "Open a PagerDuty incident and page the on-call responder.", "service_id title urgency description escalation_policy"},
	{"pagerduty_acknowledge", "Acknowledge a PagerDuty incident so paging stops.", "incident_id from_email"},
	{"pagerduty_get_oncall", "Look up who is currently on call for a schedule.", "schedule_id since until"},

	// --- cloud infrastructure ---
	{"gcs_upload_object", "Upload a local file to a Cloud Storage bucket.", "bucket object_name source_path content_type"},
	{"gcs_download_object", "Download an object from a Cloud Storage bucket to disk.", "bucket object_name destination_path"},
	{"gcs_list_objects", "List objects in a bucket under an optional prefix.", "bucket prefix delimiter max_results"},
	{"gcs_signed_url", "Generate a time-limited signed URL for an object.", "bucket object_name expiration method"},
	{"bigquery_run_query", "Execute a SQL query against BigQuery and return rows.", "query dataset max_results dry_run use_legacy_sql"},
	{"bigquery_load_table", "Load data from Cloud Storage into a BigQuery table.", "source_uris dataset table schema write_disposition"},
	{"bigquery_export_table", "Export a BigQuery table to Cloud Storage as CSV or Avro.", "dataset table destination_uri format compression"},
	{"k8s_restart_deployment", "Trigger a rolling restart of a Kubernetes deployment.", "namespace deployment cluster"},
	{"k8s_scale_deployment", "Change the replica count of a Kubernetes deployment.", "namespace deployment replicas cluster"},
	{"k8s_get_pod_logs", "Fetch logs from a pod container, optionally following.", "namespace pod container tail_lines previous"},
	{"k8s_describe_pod", "Show events, conditions and restart counts for a pod.", "namespace pod cluster"},
	{"gce_restart_instance", "Restart a Compute Engine virtual machine instance.", "project zone instance"},
	{"gce_resize_disk", "Grow the size of a persistent disk attached to an instance.", "project zone disk new_size_gb"},
	{"gce_create_snapshot", "Take a snapshot of a persistent disk for backup.", "project zone disk snapshot_name"},
	{"cloudrun_deploy_service", "Deploy a container image as a Cloud Run service revision.", "service image region env_vars cpu memory"},
	{"cloudrun_rollback", "Route traffic back to a previous Cloud Run revision.", "service region revision traffic_percent"},

	// --- identity and secrets ---
	{"iam_grant_role", "Grant an IAM role to a principal on a resource.", "resource principal role condition"},
	{"iam_revoke_role", "Remove an IAM role binding from a principal.", "resource principal role"},
	{"iam_list_permissions", "List effective permissions a principal holds on a resource.", "resource principal"},
	{"secret_rotate_key", "Rotate a service account key and disable the previous one.", "service_account key_id disable_old"},
	{"secret_read_version", "Read the payload of a secret version from Secret Manager.", "secret version project"},
	{"secret_create_version", "Add a new version to an existing secret.", "secret payload project"},

	// --- data and documents ---
	{"sheets_append_rows", "Append rows of values to a Google Sheets spreadsheet.", "spreadsheet_id range values insert_option"},
	{"sheets_read_range", "Read a cell range from a spreadsheet as values.", "spreadsheet_id range value_render_option"},
	{"docs_create_document", "Create a Google Doc with a title and initial content.", "title body folder_id"},
	{"docs_replace_text", "Find and replace text throughout a Google Doc.", "document_id find replace match_case"},
	{"drive_share_file", "Share a Drive file with a user, group or domain.", "file_id email role send_notification"},
	{"drive_search_files", "Search Drive for files by name, type or owner.", "query mime_type owner modified_after"},

	// --- misc ---
	{"transcribe_audio", "Transcribe an audio recording into text with timestamps.", "audio_uri language diarize timestamps"},
	{"translate_text", "Translate text between languages.", "text source_language target_language"},
	{"geocode_address", "Convert a street address into latitude and longitude.", "address region language"},
	{"currency_convert", "Convert an amount between two currencies at today's rate.", "amount from_currency to_currency"},
}
