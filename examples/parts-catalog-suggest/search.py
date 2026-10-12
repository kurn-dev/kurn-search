"""Checked Python journey for the local parts-catalog-suggest recipe."""
import json
import os
import re
import sys

repo = os.environ.get("KURN_SEARCH_REPO", "")
if not repo:
    sys.stderr.write("parts-suggest-search: KURN_SEARCH_REPO must name the checkout\n")
    sys.exit(2)
sys.path.insert(0, os.path.join(repo, "clients", "python"))

from kurn_search_client import KurnSearchClient, PartsCatalogSuggestRequest  # noqa: E402

FIXTURE_HANDLE = "client-token-a"
LIMIT = 25

RECORDS = {
    "PC-001": {"id": "PC-001", "identifier": "AX-100", "manufacturer": "Northstar", "category": "bearing", "status": "active", "tenant_id": "tenant-a", "partition_id": "north"},
    "PC-002": {"id": "PC-002", "identifier": "AX-100", "manufacturer": "Halden", "category": "bearing", "status": "active", "tenant_id": "tenant-a", "partition_id": "north"},
    "PC-003": {"id": "PC-003", "identifier": "AX-100", "manufacturer": "Northstar", "category": "bearing", "status": "discontinued", "tenant_id": "tenant-a", "partition_id": "south"},
    "PC-004": {"id": "PC-004", "identifier": "AX-100", "manufacturer": "Northstar", "category": "filter", "status": "active", "tenant_id": "tenant-a", "partition_id": "north"},
    "PC-006": {"id": "PC-006", "identifier": "AX-101", "manufacturer": "Northstar", "category": "bearing", "status": "active", "tenant_id": "tenant-a", "partition_id": "north"},
    "PC-007": {"id": "PC-007", "identifier": "AX-102", "manufacturer": "Vega", "category": "gasket", "status": "discontinued", "tenant_id": "tenant-a", "partition_id": "south"},
    "PC-008": {"id": "PC-008", "identifier": "BX-100", "manufacturer": "Halden", "category": "filter", "status": "active", "tenant_id": "tenant-a", "partition_id": "south"},
    "PC-009": {"id": "PC-009", "identifier": "GX-200", "manufacturer": "Vega", "category": "gasket", "status": "active", "tenant_id": "tenant-a"},
    "PC-010": {"id": "PC-010", "identifier": "AX-100", "manufacturer": "Northstar", "category": "bearing", "status": "active", "tenant_id": "tenant-a"},
    "PC-011": {"id": "PC-011", "identifier": "AX-100", "manufacturer": "northstar", "category": "bearing", "status": "active", "tenant_id": "tenant-a", "partition_id": "north"},
    "PC-012": {"id": "PC-012", "identifier": "AX-103", "manufacturer": "Northstar", "category": "Bearing", "status": "active", "tenant_id": "tenant-a", "partition_id": "north"},
}


def scored(score, *ids):
    return [{"id": identifier, "score": score} for identifier in ids]


CASES = [
    ("exact", "GX-200", None, scored(100, "PC-009")),
    ("similar", "GX-201", None, scored(93, "PC-009")),
    ("unknown query gram is excluded", "AX-1O0", None, scored(100, "PC-001", "PC-002", "PC-003", "PC-004", "PC-010", "PC-011", "PC-006", "PC-007", "PC-012")),
    ("literal scalar manufacturer", "AX-100", {"manufacturer": "Northstar"}, scored(100, "PC-001", "PC-003", "PC-004", "PC-010") + scored(78, "PC-006", "PC-012")),
    ("multi-select category", "AX-100", {"category": ["filter", "gasket"]}, [{"id": "PC-004", "score": 100}, {"id": "PC-007", "score": 78}, {"id": "PC-008", "score": 76}]),
    ("case-sensitive manufacturer", "AX-100", {"manufacturer": "northstar"}, scored(100, "PC-011")),
    ("complete empty result", "Z", None, []),
]


def fail(message, code):
    sys.stderr.write(f"parts-suggest-search: {message}\n")
    sys.exit(code)


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


address = os.environ.get("KURN_SEARCHD_ADDR", "")
if not re.fullmatch(r"127\.0\.0\.1:[0-9]{1,5}", address):
    fail("KURN_SEARCHD_ADDR must be the documented loopback address", 2)

client = KurnSearchClient("http://" + address, lambda: FIXTURE_HANDLE)
failures = 0
for name, query, filters, expected in CASES:
    try:
        result = client.search_parts_catalog_suggest(
            PartsCatalogSuggestRequest(query=query, limit=LIMIT, filters=filters), timeout_seconds=2.0)
    except Exception as error:  # noqa: BLE001 - report class only
        fail(f"{name}: request failed: {type(error).__name__}", 2)
    observed = [{"id": hit["entry_id"], "score": hit["result"].get("score")} for hit in result["hits"]]
    values_match = all(
        hit["entry_id"] in RECORDS
        and isinstance(hit["result"].get("score"), int)
        and canonical(hit["result"]) == canonical({**RECORDS[hit["entry_id"]], "score": hit["result"]["score"]})
        for hit in result["hits"]
    )
    ok = result.get("complete") is True and observed == expected and values_match
    status = "ok" if ok else "MISMATCH"
    description = ", ".join(f"{item['id']}:{item['score']}" for item in observed)
    print(f"{status}  {name}: {description}")
    if not ok:
        failures += 1

if failures:
    fail(f"{failures} case(s) did not match the checked expectation", 1)
