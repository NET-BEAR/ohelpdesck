# Architecture

HTTP boundary получает authenticated principal из existing auth session, проверяет CSRF для mutation, global permission and locked Channel membership before forwarding provider-neutral core command. Resource policy must execute within the transaction owning transition so revoked membership wins before write. API maps domain errors: unauthenticated 401, forbidden 403, validation 400, version conflict 409, missing Conversation 404. OpenAPI changes are mandatory.
