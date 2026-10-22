### Watch leak

The controller leaks **watches** on restart. See [the runbook](https://wiki.example.com/x) and @jdoe.

#### Steps
1. Restart the controller with `kubectl rollout restart`
1. Count watches
   1. per namespace
- Seen on _staging_ only

```bash
kubectl get pods -o name | wc -l
echo *not bold*
```

> It happened again.


> Logs show **many** reconnects.


||Env||Leaks||
|staging|yes|
---
Related: <https://jira.example.com/browse/PLAT-100>
