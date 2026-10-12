# Manual Fixture Controls

These two preserved blocks describe the ordinary one-record identifier
fixture produced by the local helper. They are checked byte-for-byte by the
public helper tests, not a production authentication design. Use the helper
in the beginner walkthrough instead of hand-editing bound controls.

<!-- w6s0:local-inputs:start -->
```sh
printf '%s' 'unused-local-backend-token' \
  > "$KURN_SEARCH_RUN_ROOT/backend-token"

printf '%s\n' \
  '{"schema":"kurn-search-fixture-auth/v1","credentials":[{"handle":"client-token","tenant":"tenant-proof"}]}' \
  > "$KURN_SEARCH_RUN_ROOT/fixture-auth.json"

printf '%s\n' \
  '{"schema":"kurn-search-readiness/v1","goldens":[{"name":"example-ready","route":"catalog-identifier","auth_handle":"client-token","sentinel_tenant":"tenant-proof","query":"ABC-123","partition_id":"partition-proof","limit":25,"expected_ids":["config-proof"],"expected_complete":true}]}' \
  > "$KURN_SEARCH_RUN_ROOT/readiness.json"
```
<!-- w6s0:local-inputs:end -->

<!-- w6s0:service-config:start -->
```sh
cat > "$KURN_SEARCH_RUN_ROOT/kurn-searchd.json" <<EOF
{"schema":"kurn-search-private-local-proof/v1","listen_address":"$KURN_SEARCHD_ADDR","runtime_status_file":"$KURN_SEARCH_RUN_ROOT/search-status.json","kurn_endpoint":"http://$KURND_ADDR","backend_credential_file":"$KURN_SEARCH_RUN_ROOT/backend-token","kurn_source_revision":"06c97f9fe695e29ce5adfc05f502edd68f5790e2","rate":{"unlimited":true,"rate_per_sec":0},"adapter":{"name":"catalog-identifier","physical_list_name":"catalog-identifiers-v1","config_sha256":"ffd99df8acdeb32535e52dd60bfcf02f8bfa1f0968b2895ba7a48a4c1fb887f6","timeout_millis":30000,"max_inflight":1,"worst_case_charge_bytes":1073741824},"fixture_authenticator_file":"$KURN_SEARCH_RUN_ROOT/fixture-auth.json","readiness_file":"$KURN_SEARCH_RUN_ROOT/readiness.json"}
EOF

chmod 600 \
  "$KURN_SEARCH_RUN_ROOT/backend-token" \
  "$KURN_SEARCH_RUN_ROOT/fixture-auth.json" \
  "$KURN_SEARCH_RUN_ROOT/readiness.json" \
  "$KURN_SEARCH_RUN_ROOT/kurn-searchd.json"
```
<!-- w6s0:service-config:end -->
