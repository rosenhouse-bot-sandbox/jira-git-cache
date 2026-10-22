# PLAT-123: Controller leaks watches on restart

- Status: In Progress
- Resolution: Unresolved
- Type: Bug
- Priority: High
- Assignee: Jane Doe
- Reporter: John Smith
- Labels: controller, k8s
- Parent: PLAT-100
- Links: blocks PLAT-130, relates to INFRA-77
- Created: 2026-10-01T14:02:11Z
- Updated: 2026-10-20T09:15:40Z
- URL: https://jira.example.com/browse/PLAT-123
- Epic Link: PLAT-90
- Parent Link:
- Reviewers: Jane Doe
- Sprint: Sprint 41, Sprint 42
- Story Points: 3
- Team: Platform

## Description

#### Repro
* restart controller
`kubectl ...`

## Comments

### Jane Doe — 2026-10-19T17:30:02Z

Repro'd on **staging**.
```
kubectl get pods
```

### John Smith — 2026-10-20T08:00:00Z

Fixed.
