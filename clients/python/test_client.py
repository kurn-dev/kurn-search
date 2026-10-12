import threading
import time
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from kurn_search_client import (
    ApiError, ClientError, KurnSearchClient, PartsCatalogDiscoverFacetAlternativesRequest, PartsCatalogDiscoverFacetsRequest,
    PartsCatalogDiscoverRequest, PartsCatalogSelectRequest, PartsCatalogSuggestRequest,
    ProtocolError, SearchRequest,
    verify_success,
)


SUCCESS = {
    "adapter": {"name": "catalog-identifier", "version": "v1", "behavior_sha256": "a" * 64},
    "complete": True,
    "hits": [],
    "lists": [{"role": "catalog", "name": "catalog-v1", "stamp": "opaque"}],
    "version_attempt": 1,
    "pool_step": 1,
    "pool": 25,
    "timing_us": {"backend": 1, "service": 2, "residual": 1},
}


class Transport:
    def __init__(self, status=200, headers=None, body=b"{}"):
        self.status = status
        self.headers = headers or {"content-type": "application/json"}
        self.body = body
        self.calls = []

    def request(self, scheme, authority, path, body, headers, timeout_seconds, cancellation):
        self.calls.append((scheme, authority, path, body, headers, timeout_seconds))
        return self.status, self.headers, self.body


class ClientTests(unittest.TestCase):
    def test_discover_route_membership_and_distinct_request_class(self):
        import json
        auth_calls = 0

        def token():
            nonlocal auth_calls
            auth_calls += 1
            return "fixture-token"

        response = {
            **SUCCESS,
            "adapter": {**SUCCESS["adapter"], "name": "parts-catalog-discover"},
            "hits": [{"entry_id": "PC-001", "result": {
                "id": "PC-001", "identifier": "AX-100", "name": "Deep groove ball bearing 6204"}}],
        }
        transport = Transport(body=json.dumps(response).encode())
        client = KurnSearchClient("http://unit.invalid/prefix", token, transport)
        result = client.search_parts_catalog_discover(
            PartsCatalogDiscoverRequest(
                "ball bearing", 25,
                filters={"manufacturer": ["Northstar", "Halden"], "status": "active"}),
            timeout_seconds=1)
        self.assertEqual(transport.calls[0][2], "/prefix/v1/search/parts-catalog-discover")
        self.assertEqual(
            transport.calls[0][3],
            b'{"filters":{"manufacturer":["Northstar","Halden"],"status":"active"},"limit":25,"query":"ball bearing"}')
        self.assertEqual(result["hits"][0]["result"]["name"], "Deep groove ball bearing 6204")
        with self.assertRaises(ClientError):
            client.search_parts_catalog_discover(
                PartsCatalogDiscoverRequest("ball", 25, filters={"category": []}), timeout_seconds=1)
        with self.assertRaises(ClientError):
            client.search_parts_catalog_discover(
                PartsCatalogSelectRequest("ball", 25), timeout_seconds=1)
        self.assertEqual(auth_calls, 1)
        self.assertEqual(len(transport.calls), 1)

    def test_discover_facets_route_strict_summary_and_refusals(self):
        import json
        response = {
            **SUCCESS,
            "adapter": {**SUCCESS["adapter"], "name": "parts-catalog-discover-facets"},
            "hits": [{"entry_id": "PC-007", "result": {
                "id": "PC-007", "identifier": "AX-102", "name": "Flange gasket 80 mm"}}],
            "total_matches": 2,
            "facets": {
                "category": [{"count": 2, "value": "gasket"}],
                "manufacturer": [{"count": 1, "value": "Halden"}, {"count": 1, "value": "Vega"}],
                "status": [{"count": 1, "value": "active"}, {"count": 1, "value": "discontinued"}],
            },
        }
        transport = Transport(body=json.dumps(response).encode())
        client = KurnSearchClient("http://unit.invalid/prefix", lambda: "fixture-token", transport)
        result = client.search_parts_catalog_discover_facets(
            PartsCatalogDiscoverFacetsRequest("gasket", 25, filters={"status": ["active", "discontinued"]}),
            timeout_seconds=1)
        self.assertEqual(transport.calls[0][2], "/prefix/v1/search/parts-catalog-discover-facets")
        self.assertEqual(result["total_matches"], 2)
        self.assertEqual(result["facets"]["manufacturer"],
                         [{"count": 1, "value": "Halden"}, {"count": 1, "value": "Vega"}])
        drifted = dict(response)
        drifted["facets"] = {"category": [{"count": 2, "value": "gasket"}]}
        drifting = KurnSearchClient("http://unit.invalid", lambda: "fixture-token",
                                    Transport(body=json.dumps(drifted).encode()))
        with self.assertRaises(ProtocolError):
            drifting.search_parts_catalog_discover_facets(
                PartsCatalogDiscoverFacetsRequest("gasket", 25), timeout_seconds=1)
        with self.assertRaises(ClientError):
            client.search_parts_catalog_discover_facets(
                PartsCatalogDiscoverFacetsRequest("gasket", 25, filters={"category": []}),
                timeout_seconds=1)

    def test_discover_facets_ceiling_classifies_existing_422(self):
        import json
        transport = Transport(body=b'{"category":"incomplete_at_pool_ceiling"}', status=422)
        client = KurnSearchClient("http://unit.invalid", lambda: "fixture-token", transport)
        with self.assertRaises(ApiError) as caught:
            client.search_parts_catalog_discover_facets(
                PartsCatalogDiscoverFacetsRequest("gasket", 25), timeout_seconds=1)
        self.assertEqual(caught.exception.value.status, 422)
        self.assertEqual(caught.exception.value.category, "incomplete_at_pool_ceiling")

    def test_discover_alternatives_route_bases_and_refusals(self):
        import json
        response = {
            **SUCCESS,
            "adapter": {**SUCCESS["adapter"], "name": "parts-catalog-discover-facet-alternatives"},
            "hits": [{"entry_id": "PC-001", "result": {"manufacturer": "Northstar"}}],
            "total_matches": 1,
            "facets": {
                "category": {"basis_total": 1, "buckets": [{"count": 1, "value": "bearing"}]},
                "manufacturer": {"basis_total": 2, "buckets": [
                    {"count": 1, "value": "Halden"}, {"count": 1, "value": "Northstar"}]},
                "status": {"basis_total": 1, "buckets": [{"count": 1, "value": "active"}]},
            },
        }
        transport = Transport(body=json.dumps(response).encode())
        client = KurnSearchClient("http://unit.invalid/prefix", lambda: "fixture-token", transport)
        result = client.search_parts_catalog_discover_facet_alternatives(
            PartsCatalogDiscoverFacetAlternativesRequest("bearing", 25, filters={"manufacturer": "Northstar"}),
            timeout_seconds=1)
        self.assertEqual(transport.calls[0][2], "/prefix/v1/search/parts-catalog-discover-facet-alternatives")
        self.assertEqual(transport.calls[0][3], b'{"filters":{"manufacturer":"Northstar"},"limit":25,"query":"bearing"}')
        self.assertEqual(result["facets"]["manufacturer"]["basis_total"], 2)
        bad = dict(response)
        bad["facets"] = {**response["facets"], "manufacturer": {
            "basis_total": 2, "buckets": [
                {"count": 1, "value": "Northstar"}, {"count": 1, "value": "Halden"}]}}
        drifting = KurnSearchClient("http://unit.invalid", lambda: "fixture-token",
                                    Transport(body=json.dumps(bad).encode()))
        with self.assertRaises(ProtocolError):
            drifting.search_parts_catalog_discover_facet_alternatives(
                PartsCatalogDiscoverFacetAlternativesRequest("bearing", 25), timeout_seconds=1)
        with self.assertRaises(ClientError):
            client.search_parts_catalog_discover_facet_alternatives(
                PartsCatalogDiscoverFacetAlternativesRequest("bearing", 25, filters={"category": []}),
                timeout_seconds=1)
        self.assertEqual(len(transport.calls), 1)

    def test_suggest_route_membership_and_distinct_request_class(self):
        import json
        auth_calls = 0

        def token():
            nonlocal auth_calls
            auth_calls += 1
            return "fixture-token"

        response = {
            **SUCCESS,
            "adapter": {**SUCCESS["adapter"], "name": "parts-catalog-suggest"},
            "hits": [{"entry_id": "PC-009", "result": {
                "id": "PC-009", "identifier": "GX-200", "score": 93}}],
        }
        transport = Transport(body=json.dumps(response).encode())
        client = KurnSearchClient("http://unit.invalid/prefix", token, transport)
        result = client.search_parts_catalog_suggest(
            PartsCatalogSuggestRequest(
                "GX-201", 25,
                filters={"category": ["gasket", "filter"], "status": "active"}),
            timeout_seconds=1)
        self.assertEqual(transport.calls[0][2], "/prefix/v1/search/parts-catalog-suggest")
        self.assertEqual(
            transport.calls[0][3],
            b'{"filters":{"category":["gasket","filter"],"status":"active"},"limit":25,"query":"GX-201"}')
        self.assertEqual(result["hits"][0]["result"]["score"], 93)
        for filters in ({"category": []}, {"status": ["active", "retired"]}):
            with self.assertRaises(ClientError) as caught:
                client.search_parts_catalog_suggest(
                    PartsCatalogSuggestRequest("GX-201", 25, filters=filters),
                    timeout_seconds=1)
            self.assertEqual(caught.exception.reason, "invalid_request")
        with self.assertRaises(ClientError):
            client.search_parts_catalog_suggest(
                PartsCatalogSelectRequest("GX-201", 25), timeout_seconds=1)
        self.assertEqual(auth_calls, 1)
        self.assertEqual(len(transport.calls), 1)

    def test_select_arrays_preserve_input_and_refuse_before_io(self):
        import json
        auth_calls = 0
        def token():
            nonlocal auth_calls
            auth_calls += 1
            return "fixture-token"
        transport = Transport(body=json.dumps(SUCCESS).encode())
        client = KurnSearchClient("http://unit.invalid/prefix", token, transport)
        for sequence in (list, tuple):
            filters = {"status": sequence(["discontinued", "active"]),
                       "manufacturer": sequence(["Northstar", "Halden", "Northstar"]), "category": "bearing"}
            client.search_parts_catalog_select(PartsCatalogSelectRequest("AX-100", 25, "north", "v1", filters), timeout_seconds=1)
            self.assertEqual(transport.calls[-1][2], "/prefix/v1/search/parts-catalog-select")
            self.assertEqual(transport.calls[-1][3], b'{"adapter_version":"v1","filters":{"category":"bearing","manufacturer":["Northstar","Halden","Northstar"],"status":["discontinued","active"]},"limit":25,"partition_id":"north","query":"AX-100"}')
        for filters in ({"category": ["bearing"]}, {"status": ["active", "active"]},
                        {"category": ["😀" * 512] * 16, "manufacturer": ["x"] * 16, "status": ["active", "discontinued"]}):
            client.search_parts_catalog_select(PartsCatalogSelectRequest("AX-100", 25, filters=filters), timeout_seconds=1)
            self.assertEqual(json.loads(transport.calls[-1][3])["filters"], filters)
        invalid = [[], True, "bearing", {"tenant_id": "tenant-a"}, {"partition_id": "north"}, {"unknown": None},
                   {"status": "retired"}, {"status": ["active", "retired"]}, {"status": ["active"] * 3},
                   {"category": ["bearing"] * 17}, {"manufacturer": ["Northstar"] * 17}]
        for name in ("category", "manufacturer", "status"):
            for value in ([], [None], [1], [False], [["bearing"]], [{}], [""], ["😀" * 513],
                          [chr(0xD800)], [chr(0xDC00)], {"active"}, iter(["active"]), b"active"):
                invalid.append({name: value})
        for filters in invalid:
            with self.assertRaises(ClientError) as caught:
                client.search_parts_catalog_select(PartsCatalogSelectRequest("AX-100", 25, filters=filters), timeout_seconds=1)
            self.assertEqual(caught.exception.reason, "invalid_request")
        self.assertEqual(len(invalid), 50)
        self.assertEqual(auth_calls, 5)
        self.assertEqual(len(transport.calls), 5)
        # Dataclasses are deliberately distinct: the legacy method does not
        # acquire an array capability just because its route string is new.
        with self.assertRaises(ClientError):
            client.search("parts-catalog-select", PartsCatalogSelectRequest("AX-100", 25), timeout_seconds=1)
        with self.assertRaises(ClientError):
            client.search_parts_catalog_select(SearchRequest("AX-100", 25), timeout_seconds=1)
        self.assertEqual(auth_calls, 5)

    def test_legacy_signature_bytes_and_refusals_match_delivered_s2(self):
        import inspect
        import json
        from pathlib import Path
        import hashlib
        import sys
        import types
        source = (Path(__file__).resolve().parent / "testdata" / "s2-client.py").read_bytes()
        self.assertEqual(hashlib.sha256(source).hexdigest(),
                         "8c0ab875cfe3d3d133801c911e361fefe015a20f28121336da5b3a6fb8ed169c")
        old = types.ModuleType("_kurn_s2_client_snapshot")
        sys.modules[old.__name__] = old
        try:
            exec(compile(source, "<delivered-s2-client>", "exec"), old.__dict__)
            self.assertEqual(str(inspect.signature(SearchRequest)), str(inspect.signature(old.SearchRequest)))
            self.assertEqual(SearchRequest.__annotations__, old.SearchRequest.__annotations__)
            self.assertEqual(list(inspect.signature(PartsCatalogSelectRequest).parameters), list(inspect.signature(SearchRequest).parameters))
            self.assertEqual(list(inspect.signature(PartsCatalogSuggestRequest).parameters), list(inspect.signature(SearchRequest).parameters))
            shapes = (None, {}, {"category": "bearing"}, {"status": "retired"},
                      {"manufacturer": '\x0b<é😀\u2028/\\"', "category": " bearing "}, [],
                      {"category": ["bearing"]}, {"status": ["active", "discontinued"]},
                      {"category": chr(0xD800)}, {"category": "x" * 513}, {"tenant_id": "tenant-a"})
            comparisons = 0
            for route in ("parts-catalog", "catalog-identifier", "parts-catalog-select"):
                for filters in shapes:
                    def observe(Client, Request):
                        auth = 0
                        def token():
                            nonlocal auth
                            auth += 1
                            return "fixture-token"
                        transport = Transport(body=json.dumps(SUCCESS).encode())
                        client = Client("http://unit.invalid", token, transport)
                        reason = None
                        try:
                            client.search(route, Request("AX-100", 25, "north", "v1", filters), timeout_seconds=1)
                        except Exception as error:
                            reason = (type(error).__name__, getattr(error, "reason", None))
                        return auth, transport.calls, reason
                    self.assertEqual(observe(KurnSearchClient, SearchRequest), observe(old.KurnSearchClient, old.SearchRequest))
                    comparisons += 1
            self.assertEqual(comparisons, 33)
        finally:
            del sys.modules[old.__name__]

    def test_fixed_methods_retain_transport_errors_no_retry_and_optional_verification(self):
        import json
        for method in ("legacy", "select", "suggest"):
            def invoke(client, **kwargs):
                if method == "legacy":
                    return client.search("parts-catalog", SearchRequest("AX-100", 25), **kwargs)
                if method == "select":
                    return client.search_parts_catalog_select(
                        PartsCatalogSelectRequest("AX-100", 25), **kwargs)
                return client.search_parts_catalog_suggest(
                    PartsCatalogSuggestRequest("AX-100", 25), **kwargs)
            for status, body, headers, expected, reason in (
                (307, b"{}", {}, ProtocolError, "redirect"),
                (503, b'{"category":"backend_overloaded"}', {"content-type": "application/json"}, ApiError, "backend_overloaded"),
                (429, b'{"category":"backend_rate_limited","retry_after_seconds":2}', {"content-type": "application/json", "retry-after": "2"}, ApiError, "backend_rate_limited"),
                (200, b"{", {"content-type": "application/json"}, ProtocolError, "malformed_json"),
            ):
                transport = Transport(status=status, body=body, headers=headers)
                client = KurnSearchClient("http://unit.invalid", lambda: "fixture-token", transport)
                with self.assertRaises(expected) as caught:
                    invoke(client, timeout_seconds=1)
                self.assertEqual(caught.exception.value.category if isinstance(caught.exception, ApiError) else caught.exception.reason, reason)
                self.assertEqual(len(transport.calls), 1)
            transport = Transport(body=json.dumps({**SUCCESS, "hits": [{"entry_id": "same", "result": {}}, {"entry_id": "same", "result": {}}]}).encode())
            auth = 0
            def token():
                nonlocal auth
                auth += 1
                return "fixture-token"
            client = KurnSearchClient("http://unit.invalid", token, transport)
            self.assertEqual(verify_success(invoke(client, timeout_seconds=1)), ("duplicate_entry_id",))
            cancellation = threading.Event()
            cancellation.set()
            for options, reason in (({"timeout_seconds": 1, "cancellation": cancellation}, "cancelled"), ({"timeout_seconds": 0}, "invalid_timeout")):
                with self.assertRaises(ClientError) as caught:
                    invoke(client, **options)
                self.assertEqual(caught.exception.reason, reason)
            self.assertEqual(auth, 1)
            self.assertEqual(len(transport.calls), 1)

    def test_filter_bytes_preserve_omission_presence_literals_and_order(self):
        import json
        transport = Transport(body=json.dumps(SUCCESS).encode())
        client = KurnSearchClient("http://unit.invalid", lambda: "fixture-token", transport)
        # The old four positional fields retain their original meanings.
        for request in (SearchRequest("AX-100", 25, "north", "v1"),
                        SearchRequest("AX-100", 25, "north", "v1", None)):
            client.search("parts-catalog", request, timeout_seconds=1)
            self.assertEqual(transport.calls[-1][3],
                b'{"adapter_version":"v1","limit":25,"partition_id":"north","query":"AX-100"}')
        client.search("parts-catalog", SearchRequest("AX-100", 25, "north", "v1", {}), timeout_seconds=1)
        self.assertEqual(transport.calls[-1][3],
            b'{"adapter_version":"v1","filters":{},"limit":25,"partition_id":"north","query":"AX-100"}')
        filters = {"status": "active", "manufacturer": "Northstar", "category": "bearing"}
        client.search("parts-catalog", SearchRequest("AX-100", 25, "north", "v1", filters), timeout_seconds=1)
        self.assertEqual(transport.calls[-1][3],
            b'{"adapter_version":"v1","filters":{"category":"bearing","manufacturer":"Northstar","status":"active"},"limit":25,"partition_id":"north","query":"AX-100"}')
        self.assertEqual(list(filters), ["status", "manufacturer", "category"])
        for name in ("category", "manufacturer", "status"):
            for value in ("unknown", " Northstar ", "😀" * 512, 'é\n"\\\x01'):
                client.search("parts-catalog", SearchRequest("AX-100", 25, filters={name: value}), timeout_seconds=1)
                # Independently encode the expected wire envelope. This fixture
                # uses U+0001, whose escape has no hex letter casing difference.
                expected = json.dumps({"filters": {name: value}, "limit": 25, "query": "AX-100"},
                                      ensure_ascii=False, separators=(",", ":")).encode()
                self.assertEqual(transport.calls[-1][3], expected)
        self.assertEqual(len(transport.calls), 16)

    def test_closed_filter_grammar_refuses_before_authentication_and_transport(self):
        auth_calls = 0
        transport = Transport()
        def token():
            nonlocal auth_calls
            auth_calls += 1
            return "fixture-token"
        client = KurnSearchClient("http://unit.invalid", token, transport)
        # Outer None is intentionally omission in this API, not wire null.
        invalid = [True, 1, "active", [], {"tenant_id": "tenant-a"},
                   {"partition_id": "north"}, {"unknown": None}, {1: "bearing"}]
        for name in ("category", "manufacturer", "status"):
            for value in (None, False, 1, [], {}, "", "😀" * 513, chr(0xD800), chr(0xDC00)):
                invalid.append({name: value})
        for filters in invalid:
            with self.assertRaises(ClientError) as caught:
                client.search("parts-catalog", SearchRequest("AX-100", 25, filters=filters), timeout_seconds=1)
            self.assertEqual(caught.exception.reason, "invalid_request")
        self.assertEqual(len(invalid), 35)
        self.assertEqual(auth_calls, 0)
        self.assertEqual(transport.calls, [])

    def test_canonical_request_and_additive_success(self):
        import json
        body = json.dumps({**SUCCESS, "future": {"value": True}}, separators=(",", ":")).encode()
        transport = Transport(body=body)
        client = KurnSearchClient("http://unit.invalid/prefix", lambda: "fixture-token", transport)
        result = client.search("catalog-identifier", SearchRequest(
            query="AX-104\n", limit=25, partition_id="p-é", adapter_version="v1"), timeout_seconds=1)
        self.assertEqual(transport.calls[0][2], "/prefix/v1/search/catalog-identifier")
        self.assertEqual(transport.calls[0][3],
            b'{"adapter_version":"v1","limit":25,"partition_id":"p-\xc3\xa9","query":"AX-104\\n"}')
        self.assertEqual(result["hits"], [])

    def test_api_error_and_protocol_error(self):
        transport = Transport(status=429,
            headers={"content-type": "application/json", "retry-after": "2"},
            body=b'{"category":"backend_rate_limited","retry_after_seconds":2}')
        client = KurnSearchClient("http://unit.invalid", lambda: "fixture-token", transport)
        with self.assertRaises(ApiError) as caught:
            client.search("catalog-identifier", SearchRequest("AX-104", 1), timeout_seconds=1)
        self.assertEqual(caught.exception.value.retry_after_seconds, 2)

        transport = Transport(status=500, headers={"content-type": "text/plain"}, body=b"opaque")
        client = KurnSearchClient("http://unit.invalid", lambda: "fixture-token", transport)
        with self.assertRaises(ProtocolError) as caught:
            client.search("catalog-identifier", SearchRequest("AX-104", 1), timeout_seconds=1)
        self.assertEqual((caught.exception.status, caught.exception.reason), (500, "non_json_media"))

    def test_pre_cancel_has_zero_auth_and_io(self):
        auth_calls = 0
        transport = Transport()

        def token():
            nonlocal auth_calls
            auth_calls += 1
            return "fixture-token"

        cancellation = threading.Event()
        cancellation.set()
        client = KurnSearchClient("http://unit.invalid", token, transport)
        with self.assertRaises(ClientError) as caught:
            client.search("catalog-identifier", SearchRequest("AX-104", 1),
                          timeout_seconds=1, cancellation=cancellation)
        self.assertEqual(caught.exception.reason, "cancelled")
        self.assertEqual(auth_calls, 0)
        self.assertEqual(transport.calls, [])

    def test_semantic_verifier(self):
        candidate = {**SUCCESS,
            "hits": [{"entry_id": "same", "result": {}}, {"entry_id": "same", "result": {}}],
            "lists": [{"role": "z", "name": "z", "stamp": "1"},
                      {"role": "a", "name": "a", "stamp": "2"}]}
        self.assertEqual(verify_success(candidate), ("canonical_list_order", "duplicate_entry_id"))
        self.assertEqual(verify_success(
            {"status": 429, "category": "backend_rate_limited", "retry_after_seconds": 2},
            retry_after_header="3"), ("retry_hint_eligibility",))
        self.assertEqual(verify_success(
            {"status": 400, "category": "unauthenticated"}),
            ("status_category_header_coupling",))

    def test_auth_failure_and_endpoint_are_redacted_pre_io(self):
        transport = Transport()

        def token():
            raise RuntimeError("private-auth-detail")

        client = KurnSearchClient("http://unit.invalid", token, transport)
        with self.assertRaises(ClientError) as caught:
            client.search("catalog-identifier", SearchRequest("AX-104", 1), timeout_seconds=1)
        self.assertEqual(caught.exception.reason, "authentication_failure")
        self.assertNotIn("private-auth-detail", str(caught.exception))
        self.assertEqual(transport.calls, [])
        for endpoint in ("relative", "http://user@unit.invalid",
                         "http://unit.invalid?query", "http://unit.invalid/#fragment",
                         "http://bad host"):
            with self.assertRaises(ClientError):
                KurnSearchClient(endpoint, lambda: "fixture-token", transport)

    def test_shipped_transport_active_cancellation_is_socket_timeout_bounded(self):
        class SlowHandler(BaseHTTPRequestHandler):
            def do_POST(self):
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", "2")
                self.end_headers()
                time.sleep(0.2)
                try:
                    self.wfile.write(b"{}")
                except BrokenPipeError:
                    pass

            def log_message(self, _format, *_args):
                pass

        server = ThreadingHTTPServer(("127.0.0.1", 0), SlowHandler)
        worker = threading.Thread(target=server.serve_forever)
        worker.start()
        cancellation = threading.Event()
        timer = threading.Timer(0.01, cancellation.set)
        timer.start()
        try:
            client = KurnSearchClient(
                f"http://127.0.0.1:{server.server_port}", lambda: "fixture-token")
            started = time.monotonic()
            with self.assertRaises(ClientError) as caught:
                client.search("catalog-identifier", SearchRequest("AX-104", 1),
                              timeout_seconds=0.05, cancellation=cancellation)
            self.assertEqual(caught.exception.reason, "cancelled")
            self.assertLess(time.monotonic() - started, 0.5)
        finally:
            timer.cancel()
            server.shutdown()
            server.server_close()
            worker.join()


if __name__ == "__main__":
    unittest.main()
