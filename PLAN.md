# План: комментарий на Gate возвращает задачу в Build

## Подход

Сделать fallback-маршрут Gate явным. Точные/текстовые совпадения с кнопками
(`Approve`, `Request changes`) продолжат работать как сейчас, но если ответ не
совпал ни с одной из них, `Step.Route` сначала выберет маршрут с пустым `answer`.
Если такой маршрут в custom flow не объявлен, сохранится текущая совместимость:
первый route, затем `next`, затем завершение flow.

В preset `Plan → Build → Critic → Gate` добавить скрытый fallback-route
`answer: "" → build`. Он не появится отдельной кнопкой (`GateOptions` уже
пропускает пустые labels), зато произвольный комментарий из текстового input
вернёт задачу в Build и будет передан следующему шагу как feedback. Явный
`Approve` по-прежнему завершит flow и переведёт задачу в Review.

## Файлы

- `internal/flow/flow.go` — учитывать route с пустым `answer` как явный fallback;
  обновить комментарии к контракту маршрутизации.
- `internal/flow/presets.go` — добавить стандартному Gate fallback в `build`.
- `internal/flow/flow_test.go` — зафиксировать маршрутизацию произвольного
  комментария, approve/request-changes и совместимость gate без явного fallback.
- `internal/orchestrator/flow_test.go` — интеграционно проверить путь
  `AnswerHuman` с обычным комментарием: задача снова проходит Build/Critic и
  возвращается в `needs_human`, а не оказывается в Review.
- `spec/flows.md` — кратко описать семантику явного fallback-route и поведение
  встроенного Gate.

UI менять не требуется: существующий свободный input уже отправляет текст через
тот же `AnswerHuman → resumeGate` путь, а fallback отсутствует именно в модели
маршрутизации.

## Проверка

- `go test ./internal/flow ./internal/orchestrator`
- `go test ./...`
- Убедиться тестами, что `Approve` ведёт в Review, `Request changes` и свободный
  комментарий запускают Build заново, а custom gates без пустого route сохраняют
  прежний default.
