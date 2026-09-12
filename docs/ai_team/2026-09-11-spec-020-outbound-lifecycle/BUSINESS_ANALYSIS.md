# Business rules

Same actor/key + identical request возвращает исходный Message и не создаёт новую запись/event; тот же key с отличающимся conversation/body/reply target возвращает conflict. Queued и failed reply не закрывают waiting. Первый agent `sent` очищает waiting, ставит first_response_at однократно и связывает closure message. Definitive `sent → failed` восстанавливает waiting только когда эта message всё ещё является current closure; новая waiting episode не переписывается.
