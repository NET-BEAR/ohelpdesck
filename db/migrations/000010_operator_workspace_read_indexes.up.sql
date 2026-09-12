CREATE INDEX conversations_workspace_channel_status_number_idx ON conversations(channel_id, status, number DESC, id DESC);
CREATE INDEX conversations_workspace_assignee_number_idx ON conversations(assignee_id, number DESC, id DESC) WHERE assignee_id IS NOT NULL;
CREATE INDEX channel_memberships_read_idx ON channel_memberships(user_id, channel_id) WHERE can_read;
CREATE INDEX channel_memberships_reassign_idx ON channel_memberships(channel_id, user_id) WHERE can_reassign;
