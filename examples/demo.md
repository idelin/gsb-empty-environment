# Demo document

Some prose. A plain code fence is ignored:

```sh
echo "not executed"
```

```run id=fetch defines=URL,DATA
# fetch a resource (simulated locally)
set URL https://example.invalid/data
set DATA payload-from-$URL
emit fetched $DATA
```

```run id=transform needs=fetch defines=SUMMARY
set SUMMARY summary-of($DATA)
emit transformed into $SUMMARY
```

```run id=broken needs=fetch
set PARTIAL should-not-leak
emit about to fail
fail simulated network error
```

```run id=dependent needs=broken
emit never runs
```

```run id=report needs=transform defines=REPORT
expect SUMMARY summary-of(payload-from-https://example.invalid/data)
set REPORT report: $SUMMARY via $URL
emit $REPORT
```
