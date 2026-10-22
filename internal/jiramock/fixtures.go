package jiramock

import (
	"fmt"
	"strconv"
	"time"
)

const (
	exampleBase = "https://jira.example.com"
	sprintStart = "2026-10-06T09:00:00.000Z"
	sprintEnd   = "2026-10-20T00:00:00.000Z"
	sprintClass = "com.atlassian.greenhopper.service.sprint.Sprint@1a2b"
)

// NewIssue returns a minimal open, unassigned Task. Its times are formatted in updated's location.
func NewIssue(key string, id int, updated time.Time) Issue {
	ts := updated.Format(Layout)
	return Issue{ID: strconv.Itoa(id), Key: key, Fields: map[string]any{
		"summary":     "Issue " + key,
		"status":      map[string]any{"name": "Open", "statusCategory": map[string]any{"key": "new", "name": "To Do"}},
		"resolution":  nil,
		"issuetype":   map[string]any{"name": "Task", "subtask": false},
		"priority":    map[string]any{"name": "Medium"},
		"assignee":    nil,
		"reporter":    map[string]any{"name": "jsmith", "displayName": "John Smith", "active": true},
		"labels":      []any{},
		"issuelinks":  []any{},
		"created":     ts,
		"updated":     ts,
		"description": nil,
	}}
}

func jdoe() map[string]any {
	return map[string]any{
		"self": exampleBase + "/rest/api/2/user?username=jdoe", "name": "jdoe", "key": "JIRAUSER10100",
		"emailAddress": "jdoe@example.com", "avatarUrls": map[string]any{"48x48": exampleBase + "/secure/useravatar?ownerId=JIRAUSER10100"},
		"displayName": "Jane Doe", "active": true, "timeZone": "Etc/UTC",
	}
}

// ExampleIssue returns PLAT-123 from the Jira reference, with +0200 times, CRLF text,
// one 8.x and one 10.x sprint dump, and an unset Parent Link placeholder.
func ExampleIssue() Issue {
	return Issue{ID: "10230", Key: "PLAT-123", Fields: map[string]any{
		"summary": "Controller leaks watches on restart",
		"status": map[string]any{"self": exampleBase + "/rest/api/2/status/3", "description": "", "name": "In Progress", "id": "3",
			"statusCategory": map[string]any{"id": 4.0, "key": "indeterminate", "colorName": "yellow", "name": "In Progress"}},
		"resolution": nil,
		"issuetype": map[string]any{"self": exampleBase + "/rest/api/2/issuetype/1", "id": "1", "description": "A problem.",
			"name": "Bug", "subtask": false, "avatarId": 10303.0},
		"priority": map[string]any{"self": exampleBase + "/rest/api/2/priority/2", "name": "High", "id": "2"},
		"assignee": jdoe(),
		"reporter": map[string]any{"name": "jsmith", "key": "jsmith", "displayName": "John Smith", "active": true,
			"emailAddress": "jsmith@example.com"},
		"labels": []any{"controller", "k8s"},
		"parent": map[string]any{"id": "10100", "key": "PLAT-100", "self": exampleBase + "/rest/api/2/issue/10100",
			"fields": map[string]any{"summary": "Watch reliability", "status": map[string]any{"name": "Open",
				"statusCategory": map[string]any{"key": "new", "name": "To Do"}},
				"priority": map[string]any{"name": "High"}, "issuetype": map[string]any{"name": "Story", "subtask": false}}},
		"issuelinks": []any{
			map[string]any{"id": "20001", "type": map[string]any{"id": "10000", "name": "Blocks", "inward": "is blocked by", "outward": "blocks"},
				"outwardIssue": map[string]any{"id": "10240", "key": "PLAT-130", "fields": map[string]any{"summary": "Watch cache rewrite",
					"status": map[string]any{"name": "Open"}, "priority": map[string]any{"name": "Medium"}, "issuetype": map[string]any{"name": "Task"}}}},
			map[string]any{"id": "20002", "type": map[string]any{"id": "10003", "name": "Relates", "inward": "relates to", "outward": "relates to"},
				"inwardIssue": map[string]any{"id": "30077", "key": "INFRA-77", "fields": map[string]any{"summary": "Node restarts",
					"status": map[string]any{"name": "Done"}}}},
		},
		"created":     "2026-10-01T16:02:11.000+0200",
		"updated":     "2026-10-20T11:15:40.123+0200",
		"description": "h2. Repro\r\n* restart controller\r\n{code}kubectl ...{code}",
		"customfield_10100": []any{
			Sprint8x(41, "Sprint 41", "CLOSED", ""),
			Sprint10x(42, "Sprint 42", "ACTIVE", ""),
		},
		"customfield_10002": 3.0,
		"customfield_10007": "PLAT-90",
		"customfield_10500": map[string]any{"self": exampleBase + "/rest/api/2/customFieldOption/10401", "value": "Platform", "id": "10401", "disabled": false},
		"customfield_10600": []any{jdoe()},
		"customfield_10700": map[string]any{"hasEpicLinkFieldDependency": false, "showField": false,
			"nonEditableReason": map[string]any{"reason": "PLUGIN_LICENSE_ERROR", "message": "..."}},
	}, Comments: []map[string]any{
		{"self": exampleBase + "/rest/api/2/issue/10230/comment/40001", "id": "40001", "author": jdoe(),
			"body":         "Repro'd on *staging*.\r\n{code}\r\nkubectl get pods\r\n{code}",
			"updateAuthor": jdoe(), "created": "2026-10-19T19:30:02.000+0200", "updated": "2026-10-19T19:30:02.000+0200"},
		{"self": exampleBase + "/rest/api/2/issue/10230/comment/40002", "id": "40002",
			"author": map[string]any{"name": "jsmith", "displayName": "John Smith", "active": true}, "body": "Fixed.",
			"created": "2026-10-20T10:00:00.000+0200", "updated": "2026-10-20T11:00:00.000+0200",
			"visibility": map[string]any{"type": "role", "value": "Developers"}},
	}}
}

func systemField(id, name, typ string) map[string]any {
	return map[string]any{"id": id, "name": name, "custom": false, "orderable": true, "navigable": true, "searchable": true,
		"clauseNames": []any{id}, "schema": map[string]any{"type": typ, "system": id}}
}

func customField(n int, name string, schema map[string]any) map[string]any {
	schema["customId"] = float64(n)
	return map[string]any{"id": fmt.Sprint("customfield_", n), "name": name, "custom": true, "orderable": true, "navigable": true,
		"searchable": true, "clauseNames": []any{fmt.Sprintf("cf[%d]", n), name}, "schema": schema}
}

// ExampleFields returns a GET /field body: the reference's custom fields plus system summary, status and issuetype.
func ExampleFields() []map[string]any {
	const types = "com.atlassian.jira.plugin.system.customfieldtypes:"
	return []map[string]any{
		systemField("summary", "Summary", "string"),
		systemField("status", "Status", "status"),
		systemField("issuetype", "Issue Type", "issuetype"),
		customField(10100, "Sprint", map[string]any{"type": "array", "items": "string", "custom": "com.pyxis.greenhopper.jira:gh-sprint"}),
		customField(10002, "Story Points", map[string]any{"type": "number", "custom": types + "float"}),
		customField(10007, "Epic Link", map[string]any{"type": "any", "custom": "com.pyxis.greenhopper.jira:gh-epic-link"}),
		customField(10500, "Team", map[string]any{"type": "option", "custom": types + "select"}),
		customField(10600, "Reviewers", map[string]any{"type": "array", "items": "user", "custom": types + "multiuserpicker"}),
		customField(10700, "Parent Link", map[string]any{"type": "any", "custom": "com.atlassian.jpo:jpo-custom-field-parent"}),
	}
}

// Sprint8x returns a sprint's toString dump in Jira 8.x key order.
func Sprint8x(id int, name, state, goal string) string {
	return fmt.Sprintf("%s[id=%d,rapidViewId=<null>,state=%s,name=%s,startDate=%s,endDate=%s,completeDate=<null>,sequence=%d,goal=%s]",
		sprintClass, id, state, name, sprintStart, sprintEnd, id, goal)
}

// Sprint10x returns a sprint's toString dump in Jira 10.x (alphabetical) key order.
func Sprint10x(id int, name, state, goal string) string {
	return fmt.Sprintf("%s[activatedDate=%s,autoStartStop=false,completeDate=<null>,endDate=%s,goal=%s,id=%d,"+
		"incompleteIssuesDestinationId=<null>,name=%s,rapidViewId=<null>,sequence=%d,startDate=%s,state=%s,synced=false]",
		sprintClass, sprintStart, sprintEnd, goal, id, name, id, sprintStart, state)
}
