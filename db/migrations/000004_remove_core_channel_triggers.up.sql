DROP TRIGGER IF EXISTS messages_conversation_channel_guard ON messages;
DROP TRIGGER IF EXISTS conversations_identity_channel_guard ON conversations;
DROP FUNCTION IF EXISTS verify_message_conversation_channel();
DROP FUNCTION IF EXISTS verify_conversation_identity_channel();
