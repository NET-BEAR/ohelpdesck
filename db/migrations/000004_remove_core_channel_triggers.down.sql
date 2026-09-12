CREATE FUNCTION verify_conversation_identity_channel() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM contact_identities
    WHERE id = NEW.contact_identity_id
      AND contact_id = NEW.contact_id
      AND channel_id = NEW.channel_id
  ) THEN
    RAISE EXCEPTION 'conversation_channel_mismatch' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER conversations_identity_channel_guard
  BEFORE INSERT OR UPDATE OF contact_id, contact_identity_id, channel_id ON conversations
  FOR EACH ROW EXECUTE FUNCTION verify_conversation_identity_channel();

CREATE FUNCTION verify_message_conversation_channel() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM conversations WHERE id = NEW.conversation_id AND channel_id = NEW.channel_id) THEN
    RAISE EXCEPTION 'conversation_channel_mismatch' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER messages_conversation_channel_guard
  BEFORE INSERT OR UPDATE OF conversation_id, channel_id ON messages
  FOR EACH ROW EXECUTE FUNCTION verify_message_conversation_channel();
