"""Python example for the parts-catalog recipe.

Run it after following the parts-catalog section of docs/user-manual.md:
both daemons must be up, KURN_SEARCHD_ADDR must hold the documented loopback
listener address, and KURN_SEARCH_REPO must point at the checkout so the
checked private client package can be imported without installation.

The bearer value below is the checked local fixture handle for tenant-a. It is
accepted only by the walkthrough's fixture authenticator; it is not a
credential and must not be reused as one.
"""
import json
import os
import re
import sys

repo = os.environ.get("KURN_SEARCH_REPO", "")
if not repo:
    sys.stderr.write("parts-search: KURN_SEARCH_REPO must name the checkout\n")
    sys.exit(2)
sys.path.insert(0, os.path.join(repo, "clients", "python"))

from kurn_search_client import ApiError, KurnSearchClient, SearchRequest  # noqa: E402

FIXTURE_HANDLE = "client-token-a"
ADAPTER = "parts-catalog"
QUERY = "AX-100"
LIMIT = 25

RECORDS = {
    "PC-001": {"id": "PC-001", "identifier": "AX-100", "manufacturer": "Northstar", "category": "bearing", "status": "active", "tenant_id": "tenant-a", "partition_id": "north"},
    "PC-002": {"id": "PC-002", "identifier": "AX-100", "manufacturer": "Halden", "category": "bearing", "status": "active", "tenant_id": "tenant-a", "partition_id": "north"},
    "PC-003": {"id": "PC-003", "identifier": "AX-100", "manufacturer": "Northstar", "category": "bearing", "status": "discontinued", "tenant_id": "tenant-a", "partition_id": "south"},
    "PC-004": {"id": "PC-004", "identifier": "AX-100", "manufacturer": "Northstar", "category": "filter", "status": "active", "tenant_id": "tenant-a", "partition_id": "north"},
    "PC-006": {"id": "PC-006", "identifier": "AX-101", "manufacturer": "Northstar", "category": "bearing", "status": "active", "tenant_id": "tenant-a", "partition_id": "north"},
    "PC-007": {"id": "PC-007", "identifier": "AX-102", "manufacturer": "Vega", "category": "gasket", "status": "discontinued", "tenant_id": "tenant-a", "partition_id": "south"},
    "PC-008": {"id": "PC-008", "identifier": "BX-100", "manufacturer": "Halden", "category": "filter", "status": "active", "tenant_id": "tenant-a", "partition_id": "south"},
    "PC-010": {"id": "PC-010", "identifier": "AX-100", "manufacturer": "Northstar", "category": "bearing", "status": "active", "tenant_id": "tenant-a"},
    "PC-011": {"id": "PC-011", "identifier": "AX-100", "manufacturer": "northstar", "category": "bearing", "status": "active", "tenant_id": "tenant-a", "partition_id": "north"},
    "PC-012": {"id": "PC-012", "identifier": "AX-103", "manufacturer": "Northstar", "category": "Bearing", "status": "active", "tenant_id": "tenant-a", "partition_id": "north"},
}

CASES = [
    ("unfiltered", None, ["PC-001", "PC-002", "PC-003", "PC-004", "PC-010", "PC-011", "PC-006", "PC-007", "PC-008", "PC-012"]),
    ("manufacturer=Northstar", {"manufacturer": "Northstar"}, ["PC-001", "PC-003", "PC-004", "PC-010", "PC-006", "PC-012"]),
    ("category=bearing", {"category": "bearing"}, ["PC-001", "PC-002", "PC-003", "PC-010", "PC-011", "PC-006"]),
    ("status=active", {"status": "active"}, ["PC-001", "PC-002", "PC-004", "PC-010", "PC-011", "PC-006", "PC-008", "PC-012"]),
    ("all three", {"manufacturer": "Northstar", "category": "bearing", "status": "active"}, ["PC-001", "PC-010", "PC-006"]),
    ("manufacturer=Unknown", {"manufacturer": "Unknown"}, []),
]


def fail(message, code):
    sys.stderr.write(f"parts-search: {message}\n")
    sys.exit(code)


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


address = os.environ.get("KURN_SEARCHD_ADDR", "")
if not re.fullmatch(r"127\.0\.0\.1:[0-9]{1,5}", address):
    fail("KURN_SEARCHD_ADDR must be the documented loopback address", 2)

client = KurnSearchClient("http://" + address, lambda: FIXTURE_HANDLE)
failures = 0
for name, filters, expected in CASES:
    request = SearchRequest(query=QUERY, limit=LIMIT, filters=filters)
    try:
        result = client.search(ADAPTER, request, timeout_seconds=2.0)
    except Exception as error:  # noqa: BLE001 - the example reports the class only
        fail(f"{name}: request failed: {type(error).__name__}", 2)
    hits = result["hits"]
    ids = [hit["entry_id"] for hit in hits]
    ids_match = result.get("complete") is True and ids == expected
    payloads_match = all(hit["entry_id"] in RECORDS and canonical(hit["result"]) == canonical(RECORDS[hit["entry_id"]]) for hit in hits)
    status = "ok" if ids_match and payloads_match else "MISMATCH"
    print(f"{status}  {name}: {len(ids)} hit(s) [{', '.join(ids)}]")
    if status != "ok":
        failures += 1

# The closed status vocabulary is enforced by the service: a value outside it
# is refused with HTTP 400 invalid_request before any backend work.
try:
    client.search(ADAPTER, SearchRequest(query=QUERY, limit=LIMIT, filters={"status": "retired"}), timeout_seconds=2.0)
    print("MISMATCH  status=retired: accepted")
    failures += 1
except ApiError as error:
    if error.value.status == 400 and error.value.category == "invalid_request":
        print("ok  status=retired: refused with 400 invalid_request")
    else:
        fail(f"status=retired: unexpected API error {error.value.status}", 2)
except Exception as error:  # noqa: BLE001
    fail(f"status=retired: unexpected failure: {type(error).__name__}", 2)

if failures:
    fail(f"{failures} case(s) did not match the checked expectation", 1)
