# Changelog

## v0.10.23 — 2026-07-14

- Ultraflow: Restore attention indicator for ask_human

## v0.10.22 — 2026-07-14

- Ultraflow: Отчёт для review-задач
- Ultraflow: Проверить автозапуск dev server

## v0.10.21 — 2026-07-14

- Ultraflow: Dark theme

## v0.10.20 — 2026-07-14

- Ultraflow: Task outcomes beyond "merge to main"

## v0.10.19 — 2026-07-14

- Ultraflow: Tab title shows running agent count

## v0.10.18 — 2026-07-14

- Ultraflow: Вернуть задачу в разработку после комментария
- Ultraflow: Custom dropdown component

## v0.10.17 — 2026-07-13

- Ultraflow: Refactor Orchestrator Logic

## v0.10.16 — 2026-07-13

- orchestrator: don't strand a completed-run flow task in queued after a restart

## v0.10.15 — 2026-07-13

- Ultraflow: У нас есть печа, что на карточках отображается, какая модель сейчас работает, то есть не клоп-код, а, например, клод Зонет четыре точка восемь. Вот эта фича почему-то не работает для кодекса и для клод-кода тоже узнай почему. Может быть, мы ее забыли вмержить или просто она ломается, исследуй и почини.
- Ultraflow: У нас была фича, которая позволяет в название задачи вписать большой текст, а потом это все автоматически сжимается до короткого названия. Посмотри, куда пропала эта фича и верни ее.

## v0.10.14 — 2026-07-13

- Clearer flow visibility: see at a glance what each agent is doing and where it stands.
- Board now surfaces tasks needing your attention more reliably.

## v0.10.13 — 2026-07-13

- Sharper diff contrast for clearer added/removed lines
- Rebuilt the task modal for a cleaner review layout
- Render Markdown tables properly in agent output and reports
- Tasks now resolve to the correct project when invoked over MCP
- Honest pause state: agents show as truly paused instead of faking activity

## v0.10.12 — 2026-07-13

- Refresh the board UI with a cleaner layout and interface polish.

## v0.10.11 — 2026-07-13

- Ultraflow: Skip workflow after rebase
- Ultraflow: pause all agents — global hold toggle in the TopBar
- Ultraflow: надо указывать не claude code/ codex а название модели агента. типа opus 4.8 итд

## v0.10.10 — 2026-07-12

- Ultraflow: stop wiping a resumed task's work — reuse its branch, don't re-create from main
- Ultraflow: Gate: replace dead terminal with review-focused view
- Ultraflow: В состоянии Done задача. Нажимаю на нее и там тоже почти никакой инфы. Может стоит показывать что-то?

## v0.10.9 — 2026-07-12

- Ultraflow: надо резолвить такие места как-то. то есть клод задает несколько вопросов, но это в терминале. то есть не отобразилось как needs my attention. комплексно подумай как чище всего решить эту проблему

## v0.10.8 — 2026-07-12

- docs: activity-journal — the signal:killed diagnosis + how to analyse the journal
- Ultraflow: verbose activity journal (UI clicks + task/agent lifecycle)
- Ultraflow: вот наш логотип ultraflow. используй его

## v0.10.7 — 2026-07-12

- Ultraflow: Для тасок на кодексе логотип неправильный отображаем. клодовский отображаем
- Ultraflow: restore visible 'What's new' changelog button in the redesigned TopBar
- Ultraflow: Research remote board access

## v0.10.6 — 2026-07-12

- Ultraflow: мы в paper переделали дизайн. вот он https://app.paper.design/file/01KX9P6V1FN11E1WEGHH3N9J07/1-0/3Q2-0. реализуй его. пожалуйста с нуля состаьв страницу в фронтенде, не пытайся исрпавить существующую. обычно плохо кончатеся. сразу грамтно сделай. используя скилл для реакта наш. и анимации через framer. /make-interfaces... /ux-ui-pro-max... компоненты, разделение, разбиение итд
- Ultraflow: soetimes after approving gate, task goes into plan mode again

## v0.10.5 — 2026-07-12

- Ultraflow: Audit multi-step task lifecycle
- Ultraflow: allow inputing picture in all our composers in all states. including from clipboard. use some libs

## v0.10.4 — 2026-07-12

- Ultraflow: hotkeys are broken in claude pty. maybe in codex too. for example cmd + delete.

## v0.10.3 — 2026-07-12

- Ultraflow: Audit core agentic logic & MCP

## v0.10.2 — 2026-07-12

- Ultraflow: Public + private changelog on each release cut
- Ultraflow: Flow engine: multi-step flows (M2 core)
- Ultraflow: Context cap + actualize docs

## v0.10.1 — baseline

Concise, user-facing notes on what shipped in each Ultraflow release. From the
next release on, each cut appends a laconic entry here — summarized from that
release's commits — and it shows on the board's "What's new" panel.
