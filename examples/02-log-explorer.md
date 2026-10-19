# 02 · Log explorer

Something went wrong overnight and you have a 50,000-line log. `grep` needs
you to know what to look for; hunch lets you ask what's in there first.

You'll get an overview with `pick`, rank with `score`, filter with `yes`, then
watch a live tail.

## 1. Get the shape of it

Ask what kind of event each line is and count the answers:

```sh
hunch pick "What kind of event is this?" \
  -c auth,payments,database,performance,client-error,routine \
  --tsv app.log | cut -f1 | sort | uniq -c | sort -rn
```

```text
41212 routine
 5870 client-error
 1904 auth
  611 performance
  402 payments
    1 database
```

Before a full pass, try a sample with `--stats` to see how long it takes and
how many tokens it uses:

```sh
head -500 app.log | hunch pick "What kind of event is this?" \
  -c auth,payments,database,performance,client-error,routine --stats >/dev/null
```

## 2. Zoom in

Read only one kind, as plain lines:

```sh
hunch pick "What kind of event is this?" -c auth,payments,database,performance,client-error,routine \
  --only payments --raw app.log | less
```

Or rank everything by how bad it is and read the worst first. `score` runs
from 0 (the first level) to 3 (the last) here, and can land in between, which
makes it good for sorting:

```sh
hunch score "How badly does this affect users?" -l none,minor,major,outage --top 20 app.log
```

```text
2.91 outage  2026-10-19T03:12:07Z POST /api/payments 502 upstream=stripe timeout=30s
2.88 outage  2026-10-19T03:12:09Z POST /api/payments 502 upstream=stripe timeout=30s
2.12 major   2026-10-19T03:14:40Z worker: retry queue full, dropping job id=88412
...
```

## 3. Ask the question you actually have

Now that you know where to look, ask it directly and keep confident yeses:

```sh
hunch "Is a third-party provider failing or timing out?" --min 0.8 --raw app.log
```

Find out when it started by keeping the TSV and sorting on the timestamp:

```sh
hunch "Is a third-party provider failing or timing out?" --min 0.8 --tsv app.log \
  | cut -f2 | sort | head -1
```

## 4. Spend less

hunch is cheap, but there's no need to send lines you can rule out with a
regex. Drop the obvious noise first and let hunch judge what's left:

```sh
grep -vE ' (200|204|304) ' app.log | hunch score "How badly does this affect users?" \
  -l none,minor,major,outage --min 2 --raw
```

For large files, raise concurrency with `-j 32`.

## 5. Watch it live

hunch streams: each line is judged and printed as it arrives, so it works on
a live tail:

```sh
kubectl logs -f deploy/api | hunch "Should on-call look at this right now?" \
  --yes-when "Errors that affect users, data loss, security events" \
  --no-when "Expected client errors such as 404s and validation failures" \
  --min 0.9 --raw
```

`--yes-when` and `--no-when` pin down where the line is, so expected 404s
don't page anyone.

## 6. Keep it for next time

Save the questions you keep asking as a preset in
`~/.config/hunch/config.yaml`:

```yaml
presets:
  oncall:
    context: Logs from our payments API.
    questions:
      impact: { type: score, q: "How badly does this affect users?", levels: [none, minor, major, outage] }
      vendor: { type: noul,  q: "Is a third-party provider failing or timing out?" }
    keep: impact.score >= 2 || vendor.p > 0.8
```

```sh
kubectl logs -f deploy/api | hunch @oncall --raw
```
