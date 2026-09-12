"""Narrow guard for workspace response schemas declared in api/openapi.yaml."""
from pathlib import Path
import yaml

doc = yaml.safe_load(Path("api/openapi.yaml").read_text())
schemas = doc["components"]["schemas"]

for name in ("ConversationPage", "ConversationListItem", "ConversationDetail", "MessagePage", "TimelineMessage", "WorkspaceChannel", "WorkspaceContact", "WorkspaceUser", "Conversation"):
    assert name in schemas, name

message_status = schemas["MessageStatus"]["enum"]
assert {"received", "queued", "sent", "delivered", "read", "failed", "unknown"} <= set(message_status)

for name in ("ConversationPage", "ConversationListItem", "MessagePage", "TimelineMessage", "WorkspaceChannel", "WorkspaceContact", "WorkspaceUser"):
    assert schemas[name].get("required"), name

assert schemas["ConversationPage"]["properties"]["items"]["items"]["$ref"] == "#/components/schemas/ConversationListItem"
assert schemas["MessagePage"]["properties"]["items"]["items"]["$ref"] == "#/components/schemas/TimelineMessage"
assert schemas["Conversation"]["properties"]["assignee"]["type"] == ["object", "null"]
print("workspace OpenAPI response schemas are present and internally linked")
