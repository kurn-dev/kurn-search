"""Checked Python journey for the local parts-catalog-discover-facets recipe."""
import os
import re
import sys

repo = os.environ.get("KURN_SEARCH_REPO", "")
if not repo:
    sys.stderr.write("parts-discover-facets-search: KURN_SEARCH_REPO must name the checkout\n")
    sys.exit(2)
sys.path.insert(0, os.path.join(repo, "clients", "python"))

from kurn_search_client import (  # noqa: E402
    ApiError,
    ClientError,
    KurnSearchClient,
    PartsCatalogDiscoverFacetsRequest,
)

CASES = (
    ("exact identifier and facets", "AX-100", None, 7,
     ["PC-001", "PC-002", "PC-003", "PC-010", "PC-011", "PC-004", "PC-008"],
     [[4, "Northstar"], [2, "Halden"], [1, "northstar"]]),
    ("broad word counts are pre-limit", "gasket", None, 141,
     ["PC-007", "PC-009"] + [f"ZB-{number}" for number in range(9001, 9024)],
     [[139, "Halden"], [2, "Vega"]]),
    ("counts describe the applied filter", "gasket", {"status": "discontinued"}, 2,
     ["PC-007", "ZB-9139"],
     [[1, "Halden"], [1, "Vega"]]),
    ("zero hits stay complete", "sprocket", None, 0, [], []),
)


def fail(message, code):
    sys.stderr.write(f"parts-discover-facets-search: {message}\n")
    sys.exit(code)


address = os.environ.get("KURN_SEARCHD_ADDR", "")
if not re.fullmatch(r"127\.0\.0\.1:[0-9]{1,5}", address):
    fail("KURN_SEARCHD_ADDR must be the documented loopback address", 2)

client = KurnSearchClient("http://" + address, lambda: "client-token-a")
failures = 0
for label, query, filters, total, expected_ids, expected_manufacturer in CASES:
    try:
        result = client.search_parts_catalog_discover_facets(
            PartsCatalogDiscoverFacetsRequest(query, 25, filters=filters),
            timeout_seconds=2.0,
        )
    except Exception as error:  # noqa: BLE001
        fail(f"{label}: request failed: {type(error).__name__}", 2)
    ids = [hit["entry_id"] for hit in result["hits"]]
    manufacturer = [[bucket["count"], bucket["value"]] for bucket in result["facets"]["manufacturer"]]
    ok = (result.get("complete") is True and ids == expected_ids
          and result.get("total_matches") == total and manufacturer == expected_manufacturer)
    print(f"{'ok' if ok else 'MISMATCH'}  {label}: total {result.get('total_matches')}, {len(ids)} hit(s)")
    if not ok:
        failures += 1

try:
    client.search_parts_catalog_discover_facets(
        PartsCatalogDiscoverFacetsRequest("gasket", 25, filters={"category": []}),
        timeout_seconds=2.0,
    )
    print("MISMATCH  category=[]: accepted")
    failures += 1
except ClientError:
    print("ok  category=[]: refused before transport")

try:
    client.search_parts_catalog_discover_facets(
        PartsCatalogDiscoverFacetsRequest(" gasket", 25),
        timeout_seconds=2.0,
    )
    print("MISMATCH  untrimmed query: accepted")
    failures += 1
except ApiError as error:
    if error.value.status != 400 or error.value.category != "invalid_request":
        fail("untrimmed query: unexpected failure", 2)
    print("ok  untrimmed query: HTTP 400 invalid_request")

if failures:
    fail(f"{failures} case(s) did not match", 1)
