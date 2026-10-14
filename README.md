# steve-tests

Go tests for standalone Steve: CRUD, filtering, sorting, pagination, and SQL cache behavior.

## Running tests

Requires Go 1.27.2+.

```sh
go test -p=4 -parallel=4 ./...
```

Live tests skip unless an endpoint is configured. To run them against Steve on a disposable cluster:

```sh
export STEVE_URL=https://127.0.0.1:8443
export STEVE_TEST_ALLOW_MUTATIONS=true

go test -count=1 -p=4 -parallel=4 -timeout=20m -v ./tests/...
```

Leave mutations disabled for read-only checks. Set `STEVE_TOKEN` if authentication is required, and `STEVE_CA_FILE` for a private CA. For a local self-signed endpoint, use `STEVE_INSECURE_SKIP_VERIFY=true`.

Workload tests also need `STEVE_TEST_ALLOW_WORKLOADS=true`; CRD tests need `STEVE_TEST_ALLOW_CRDS=true`. Tests clean up their resources. Cache mode is detected automatically.

Run the full suite, including mutations, workloads, and CRDs, using your config file:

```sh
STEVE_CONFIG="$HOME/steve-config.yaml" \
STEVE_TEST_ALLOW_MUTATIONS=true \
STEVE_TEST_ALLOW_WORKLOADS=true \
STEVE_TEST_ALLOW_CRDS=true \
go test -count=1 -p=4 -parallel=4 -timeout=20m -v ./...
```

These commands run up to four packages at once, with up to four parallel tests per package. Workload and CRD tests stay sequential within their packages.

Other connection settings are in [steve-config.yaml.example](steve-config.yaml.example).

In GoLand, select [**All Steve tests**](.run/All%20Steve%20tests.run.xml) in the run dropdown and click Play. It runs the full suite using `~/steve-config.yaml`, with mutations, workloads, and CRDs enabled. Each test appears in the results.

The `TestAll` play button in [all_test.go](all_test.go) also runs the suite, but GoLand reports that wrapper as one test. It skips during ordinary suite runs.

## Adding tests

- **Indexed field:** add a row to [TestSQLIndexedFields](tests/indexedfields/indexedfields_test.go). Give it two fixtures and an update. The table checks filtering, sorting, and whether the index catches up after the update.
- **Filter, sort, or pagination:** add a row to the resource's collection table. [ConfigMaps](tests/configmaps/collection_test.go) is a good starting point.
- **Different setup:** add a test in the matching resource package. Helpers in `actions/` handle fixtures, waits, and cleanup.

Run a single case with `-run`:

```sh
go test -count=1 -v ./tests/indexedfields -run 'TestSQLIndexedFields/hpa_min_replicas'
```

Keep cases independent, use explicit expected results, and clean up what you create. Use `t.Parallel()` for cases that own their fixtures; keep steps that share and change a fixture sequential.
