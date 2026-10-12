import json
import os

from kurn_search_client import (
    ApiError, ClientError, KurnSearchClient, ProtocolError, SearchRequest,
    verify_success,
)

input_value = json.loads(os.environ["KURN_S7_INPUT"])
request_value = input_value["request"]
request = SearchRequest(
    query=request_value["query"],
    limit=request_value["limit"],
    partition_id=request_value.get("partition_id"),
    adapter_version=request_value.get("adapter_version"),
    filters=request_value.get("filters"),
)
if input_value.get("flags", {}).get("unpaired_query"):
    request = SearchRequest(query=chr(0xD800), limit=request.limit,
                            partition_id=request.partition_id,
                            adapter_version=request.adapter_version, filters=request.filters)


def token():
    if input_value.get("flags", {}).get("auth_failure"):
        raise RuntimeError("private-auth-detail")
    return os.environ["KURN_S7_TOKEN"]


class Cancellation:
    def is_set(self):
        return bool(input_value.get("flags", {}).get("pre_cancel"))


client = KurnSearchClient(os.environ["KURN_S7_ENDPOINT"], token)
try:
    value = client.search(input_value["adapter"], request,
                          timeout_seconds=input_value["timeout_ms"] / 1000,
                          cancellation=Cancellation())
    output = {"kind": "success", "value": value,
              "violations": list(verify_success(value))}
except ApiError as error:
    output = {"kind": "api_error", "status": error.value.status,
              "category": error.value.category}
    retry_after_seconds = getattr(error.value, "retry_after_seconds", None)
    if retry_after_seconds is not None:
        output["retry_after_seconds"] = retry_after_seconds
except ProtocolError as error:
    output = {"kind": "protocol_error", "status": error.status,
              "reason": error.reason}
except ClientError as error:
    output = {"kind": "client_error", "reason": error.reason}
except Exception:
    output = {"kind": "client_error", "reason": "unexpected"}
print(json.dumps(output, separators=(",", ":")), end="")
