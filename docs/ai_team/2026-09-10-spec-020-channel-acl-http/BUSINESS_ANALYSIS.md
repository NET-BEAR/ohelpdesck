# Business analysis

Agent с `conversation.read`/`conversation.reply` и соответствующей Channel membership может работать с Conversation вне зависимости от `assignee_id`. Без global permission или membership capability command запрещена без mutation. Status/priority command требует reply capability, актуальный `expected_version` и CSRF.
