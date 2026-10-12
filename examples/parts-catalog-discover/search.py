"""Checked Python journey for the local parts-catalog-discover recipe."""
import os
import re
import sys

repo = os.environ.get("KURN_SEARCH_REPO", "")
if not repo:
    sys.stderr.write("parts-discover-search: KURN_SEARCH_REPO must name the checkout\n")
    sys.exit(2)
sys.path.insert(0, os.path.join(repo, "clients", "python"))

from kurn_search_client import (  # noqa: E402
    ApiError,
    ClientError,
    KurnSearchClient,
    PartsCatalogDiscoverRequest,
)

CASES = (
    ("exact identifier", "AX-100", None, None,
     ["PC-001", "PC-002", "PC-003", "PC-010", "PC-011", "PC-004", "PC-008"]),
    ("words across the name", "ball bearing", None, None,
     ["PC-001", "PC-002", "PC-003", "PC-010", "PC-011"]),
    ("name plus filter", "ball", None, {"manufacturer": "Northstar"},
     ["PC-001", "PC-003", "PC-010"]),
    ("partitioned name", "filter", "south", None, ["PC-008"]),
    ("one term cannot split across fields", "AX-bearing", None, None, []),
)


def fail(message, code):
    sys.stderr.write(f"parts-discover-search: {message}\n")
    sys.exit(code)


address = os.environ.get("KURN_SEARCHD_ADDR", "")
if not re.fullmatch(r"127\.0\.0\.1:[0-9]{1,5}", address):
    fail("KURN_SEARCHD_ADDR must be the documented loopback address", 2)

client = KurnSearchClient("http://" + address, lambda: "client-token-a")
failures = 0
for label, query, partition, filters, expected in CASES:
    try:
        result = client.search_parts_catalog_discover(
            PartsCatalogDiscoverRequest(query, 25, partition, filters=filters),
            timeout_seconds=2.0,
        )
    except Exception as error:  # noqa: BLE001
        fail(f"{label}: request failed: {type(error).__name__}", 2)
    ids = [hit["entry_id"] for hit in result["hits"]]
    shaped = all(
        hit["result"].get("id") == hit["entry_id"]
        and isinstance(hit["result"].get("identifier"), str)
        and isinstance(hit["result"].get("name"), str)
        for hit in result["hits"]
    )
    ok = result.get("complete") is True and shaped and ids == expected
    print(f"{'ok' if ok else 'MISMATCH'}  {label}: {', '.join(ids)}")
    if not ok:
        failures += 1

try:
    client.search_parts_catalog_discover(
        PartsCatalogDiscoverRequest("ball", 25, filters={"category": []}),
        timeout_seconds=2.0,
    )
    print("MISMATCH  category=[]: accepted")
    failures += 1
except ClientError:
    print("ok  category=[]: refused before transport")

try:
    client.search_parts_catalog_discover(
        PartsCatalogDiscoverRequest(" ball", 25),
        timeout_seconds=2.0,
    )
    print("MISMATCH  untrimmed query: accepted")
    failures += 1
except ApiError as error:
    if error.value.status != 400 or error.value.category != "invalid_request":
        fail("untrimmed query: unexpected API refusal", 2)
    print("ok  untrimmed query: HTTP 400 invalid_request")

if failures:
    fail(f"{failures} case(s) did not match", 1)
