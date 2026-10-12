"""Separate Python consumer over the checked local alternatives recipe."""
import os
import re
import sys

repo = os.environ.get("KURN_SEARCH_REPO", "")
address = os.environ.get("KURN_SEARCHD_ADDR", "")
if not repo or not re.fullmatch(r"127\.0\.0\.1:[0-9]{1,5}", address):
    sys.stderr.write("facet-alternatives: set KURN_SEARCH_REPO and KURN_SEARCHD_ADDR\n")
    sys.exit(2)
sys.path.insert(0, os.path.join(repo, "clients", "python"))
from kurn_search_client import (  # noqa: E402
    ApiError, ClientError, KurnSearchClient,
    PartsCatalogDiscoverFacetAlternativesRequest,
)

client = KurnSearchClient("http://" + address, lambda: "client-token-a")
cases = (
    ("selected Northstar", "bearing", {"manufacturer": "Northstar"},
     ["PC-001", "PC-003", "PC-010", "PC-006", "PC-012"], 5, 7,
     [[5, "Northstar"], [1, "Halden"], [1, "northstar"]]),
    ("unavailable selected Vega", "bearing", {"manufacturer": "Vega"},
     [], 0, 7, [[5, "Northstar"], [1, "Halden"], [1, "northstar"]]),
    ("status alternatives", "gasket", {"status": "discontinued"},
     ["PC-007", "ZB-9139"], 2, 2, [[1, "Halden"], [1, "Vega"]]),
)
failures = 0
for label, query, filters, expected_ids, total, basis, expected_buckets in cases:
    result = client.search_parts_catalog_discover_facet_alternatives(
        PartsCatalogDiscoverFacetAlternativesRequest(query, 25, filters=filters),
        timeout_seconds=2.0,
    )
    actual_ids = [hit["entry_id"] for hit in result["hits"]]
    group = result["facets"]["manufacturer"]
    buckets = [[bucket["count"], bucket["value"]] for bucket in group["buckets"]]
    ok = (actual_ids == expected_ids and result["total_matches"] == total
          and group["basis_total"] == basis and buckets == expected_buckets)
    print(f"{'ok' if ok else 'MISMATCH'}  {label}")
    failures += not ok
try:
    client.search_parts_catalog_discover_facet_alternatives(
        PartsCatalogDiscoverFacetAlternativesRequest("bearing", 25, filters={"category": []}),
        timeout_seconds=2.0)
    failures += 1
except ClientError:
    pass
try:
    client.search_parts_catalog_discover_facet_alternatives(
        PartsCatalogDiscoverFacetAlternativesRequest(" bearing", 25), timeout_seconds=2.0)
    failures += 1
except ApiError as error:
    failures += (error.value.status != 400 or error.value.category != "invalid_request")
if failures:
    sys.exit(1)
