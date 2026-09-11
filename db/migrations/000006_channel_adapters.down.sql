DO $$ BEGIN RAISE EXCEPTION 'migration 6 is irreversible: PostgreSQL enum values cannot be removed safely'; END $$;
