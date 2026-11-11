# Go-линтинг (golangci-lint) — бэкенд

Парного плана во фронте нет: фича только в `megaapp-back`. Номер 49 занят и для `megaapp-front`.

## Описание

- Ввести `golangci-lint` v2 как аналог ESLint на фронте.
- Настройки разбиты на 3 блока по серьёзности: необходимое, nice to have, вкусовщина.
- Включён только блок 1. Блоки 2 и 3 включаются по решению, по одному линтеру за раз.

## Как было

- В CI (`deploy-backend.yml`) только `go test ./...` и `go test -tags swagger ./...`.
- Ни `go vet`, ни `gofmt`-проверки, ни `-race`, ни линтера. Конфига нет.
- 136 `.go`-файлов (51 тестовый), `del/` и `old-js/` без Go-кода.

## Решение

- Конфиг `.golangci.yml` в корне `megaapp-back`, формат v2, `default: none` + явный список линтеров.
- Код с build-тегом `swagger` линтится отдельным прогоном `--build-tags swagger`. Прогоны на текущем коде дали идентичный результат.
- `errcheck`: исключены `Close` у `sql.Rows`, `sql.Stmt`, `io.ReadCloser` (тела HTTP-ответов). Идиома `defer x.Close()` на читающих хэндлах, ошибка не несёт потери данных. `Close` у писателей (файлы, архивы) остаётся под проверкой.
- Правило: проверки только дополняют разработку; выключение линтера или правила — с комментарием «почему».

## Блок 1 — необходимое (ВКЛЮЧЁН)

То, что есть в любом серьёзном Go-проекте. Ловит реальные баги, ложных срабатываний мало.

- `errcheck` — непроверенные ошибки.
- `govet` — встроенный анализатор (Printf-форматы, копии мьютексов, потерянный cancel).
- `staticcheck` — баги, deprecated API, неэффективный код (в v2 включает бывшие gosimple и stylecheck).
- `unused` — мёртвый код.
- `ineffassign` — присвоения, значение которых не используется.
- `errorlint` — сравнение ошибок через `==` и `%v` вместо `%w` ломают `errors.Is/As`.
- `bodyclose` — незакрытые тела HTTP-ответов.
- `sqlclosecheck` — незакрытые `sql.Rows`/`Stmt`.
- `rowserrcheck` — не проверенный `rows.Err()`.
- `noctx` — HTTP- и SQL-вызовы без `context`.
- `nilerr` — возврат `nil` при непустой `err`.
- `durationcheck` — умножение `Duration` на `Duration`.
- Форматтеры: `gofmt`, `goimports`.

## Блок 2 — nice to have (мнения расходятся)

Полезно, но даёт шум или спорные срабатывания. Включать по одному, после оценки числа замечаний.

- `gosec` — безопасность (SQL, слабая крипта, права файлов). Шумный: G104 дублирует errcheck, G304 реагирует на любой `os.Open(path)`.
- `contextcheck` — потеря родительского context в цепочке вызовов.
- `gocritic` — набор диагностик и стилевых проверок.
- `revive` — замена устаревшего `golint`, правила настраиваются.
- `exhaustive` — полнота `switch` по enum-подобным константам.
- `forcetypeassert` — type assertion без `ok`.
- `nilnil` — возврат `nil, nil`.
- `unparam` — неиспользуемые параметры и результаты функций.
- `unconvert` — лишние приведения типов.
- `prealloc` — срезы без предвыделения ёмкости.
- `errchkjson` — не проверенные ошибки `encoding/json`.
- `wrapcheck` — ошибки из чужих пакетов без обёртки. Очень шумный на слоях с прокидыванием ошибок.
- `perfsprint` — `fmt.Sprintf` там, где достаточно конкатенации и `strconv`.
- `usestdlibvars` — литералы вместо `http.MethodGet`, `http.StatusOK`.
- `sloglint` — единообразие вызовов `slog`.
- `thelper`, `tparallel`, `testifylint` — гигиена тестов.
- `copyloopvar`, `intrange`, `modernize` — переход на современный синтаксис Go (1.22+).
- `govet`: `enable-all` (кроме `fieldalignment`) — добавит `nilness`, `shadow`, `unusedwrite`.
- Вне golangci: `go test -race ./...` и `govulncheck ./...` в CI.

## Блок 3 — вкусовщина (причёсанность, слабо влияет на функционал)

- `gofumpt` — более строгий `gofmt`. `gci` — фиксированный порядок групп импортов.
- `misspell` — опечатки в комментариях и строках.
- `godot` — точка в конце комментариев. `godox` — запрет `TODO`/`FIXME`.
- `whitespace`, `nlreturn`, `wsl_v5` — пустые строки вокруг блоков, перед `return`.
- `lll` — длина строки.
- `gocyclo`, `gocognit`, `cyclop`, `funlen`, `nestif` — метрики сложности и размера функций.
- `dupl` — дубли кода. `goconst` — повторяющиеся строковые литералы.
- `varnamelen` — длина имён переменных. `nonamedreturns` — запрет именованных результатов.
- `mnd` — magic numbers. `gochecknoglobals`, `gochecknoinits` — запрет глобалов и `init`.
- `testpackage` — тесты в пакете `_test`. `paralleltest` — `t.Parallel()` везде.
- `exhaustruct` — все поля структур заданы явно. `ireturn`, `interfacebloat` — правила про интерфейсы.
- `depguard` — запрет импортов по списку.

## Результат первого прогона блока 1 (30.10.2026)

Всего 146 замечаний: 16 в продуктовом коде, 130 в тестах.

| Линтер | Всего | Прод | Тесты | Суть |
|---|---|---|---|---|
| `noctx` | 114 | 0 | 114 | `db.Exec`/`Query`/`QueryRow` (75), `http.NewRequest`/`Get`/`Post` (33), `httptest.NewRequest` (5), `net.Listen` (1) без context. |
| `errcheck` | 13 | 8 | 5 | `Close` у писателей/файлов: `backup/service.go` (2), `config/dotenv.go`, `food/image_pipeline.go` (2), `metrics/outbox.go`, `rotatingfile.go`, `s3/client.go`. |
| `bodyclose` | 7 | 0 | 7 | Шесть — `websocket.Dial` (тело при успешном upgrade закрывать не нужно, вероятно ложные срабатывания), одно — `flatline_client_test.go`. |
| `errorlint` | 4 | 2 | 2 | `settings/service.go` — `%v` вместо `%w` (2); `auth/service_test.go` — `!=` вместо `errors.Is` (2). |
| `unused` | 4 | 2 | 2 | `sortableKeys`, `writeFoodResultError`, тестовый `fixedEmbeddingGenerator` с методом. |
| `gofmt` | 2 | 2 | 0 | `httpx/app.go`, `httpx/legacy/legacy.go`. Автоформат. |
| `govet` | 1 | 1 | 0 | `reflect.Ptr` → `reflect.Pointer` (`settings/service.go`). |
| `staticcheck` | 1 | 1 | 0 | SA9004: у констант группы тип только на первой (`config/ingest.go`). |
| `ineffassign`, `sqlclosecheck`, `rowserrcheck`, `nilerr`, `durationcheck`, `goimports` | 0 | 0 | 0 | Чисто. |

Объём правок:

- Продуктовый код — 16 точечных правок в ~12 файлах, около часа. Содержательных две группы: `Close` у писателей (потеря ошибки записи в бэкапе и ротации файлов — реальный риск) и `%w` в `settings/service.go` (меняет цепочку ошибок).
- Автоматом: `gofmt` (2 файла).
- Тесты — 130 правок. 114 из них `noctx`, механические: `t.Context()` и `*Context`-варианты вызовов. Ценность низкая: в тестах context ничего не отменяет.

## Решения, ожидающие пользователя

- `noctx` для `_test.go`: исключить через `exclusions` или править все 114. Предложение: исключить для тестов, в продуктовом коде оставить.
- `bodyclose` на `websocket.Dial`: исключить для тестов или закрывать `response.Body` явно.

## Чек-лист

- ✅ Установлен `golangci-lint` 2.15 (Homebrew)
- ✅ Написан `.golangci.yml` с блоком 1
- ✅ Прогон блока 1, оценка объёма (с тегом `swagger` и без)
- ⭕ Решение по `noctx` и `bodyclose` в тестах
- ⭕ Правки продуктового кода (16)
- ⭕ Правки тестов
- ⭕ Шаг `golangci-lint run` (оба варианта тегов) и `go test -race` в `deploy-backend.yml`
- ⭕ Дописать правило «проверки после изменений» для `megaapp-back` в `AGENTS.md`: `go build`, `go vet`, `golangci-lint run`, `go test`
- ⭕ Оценка блока 2: прогон по одному линтеру, отбор

## Исключение noctx в тестах и поправка счёта (30.10.2026)

- Решение: `noctx` отключён для `_test.go` через `exclusions` в `.golangci.yml`. В продуктовом коде остаётся. Причина: в тестах (SQLite в памяти, миллисекунды) context ничего не отменяет, массовая правка стала бы формальностью (подходит любой context). Практика большинства проектов. Заменяет пункт «Решения, ожидающие пользователя» по `noctx`.
- Поправка таблицы первого прогона: `rowserrcheck` показан как 0, но на деле 3 (все в `food/search_personal_service_test.go`). golangci-lint оставляет одно замечание на строку (`uniq-by-line`), и `noctx` на тех же строках `db.Query` его перекрывал. После отключения `noctx` всплыло.
- Итого после отключения: 35 замечаний (16 в проде, 19 в тестах).
- `bodyclose` в тестах — ложные срабатывания: 6 на `websocket.Dial` (gorilla: тело закрывать не нужно), 1 на `flatline_client_test.go` (на не-OK ответе тело закрывает сам `History`).

- ✅ Исключение `noctx` для тестов в конфиге
- ✅ Решение по `bodyclose` в тестах (ложные срабатывания): отключён для `_test.go`
- ✅ Правки продуктового кода (16)
- ✅ Правки тестов (19, без `bodyclose`)

## Применение блока 1 (30.10.2026)

- Результат: `golangci-lint run` — 0 замечаний (с тегом `swagger` и без), `go vet`, сборка и `go test ./...` (с тегом и без) зелёные.
- `Close` у записываемых файлов (`backup/service.go`, `encodeWebP`, `encodePNG`) теперь закрывается явно в конце функции с возвратом ошибки; `defer` остаётся для путей с ошибкой. Выбрано вместо `errors.Join` в `defer`: тип возвращаемой ошибки не меняется.
- `Close` у читаемых файлов (5 мест) записан как `_ = f.Close()` в `defer`.
- Удалены мёртвые `sortableKeys`, `writeFoodResultError`, тестовый `fixedEmbeddingGenerator`.
- `bodyclose` отключён для тестов: ложные срабатывания на `websocket.Dial` и на ветке не-OK у `History`.

- ✅ Прогон `golangci-lint`, `go vet`, сборки и тестов после правок

## Линтер вне деплоя (30.10.2026)

- Решение: линтер в `deploy-backend.yml` не добавляется (заменяет пункт про CI в чек-листе выше). Проверка идёт локально, после каждой серии правок, через агента. Причины: проект одиночный, агент и так гоняет все проверки, красный линтер не должен блокировать срочный деплой в `release`. Как у фронта (ESLint в сборку не входит).
- Правило для агентов: раздел «Проверки после изменений в `megaapp-back`» в корневом `AGENTS.md` (линтер и тесты в двух вариантах — без тега и с `swagger`, плюс сборка в `bin/`). Общее правило в «Error Validation» глобальных инструкций (`~/.claude/CLAUDE.md` и зеркало `~/.codex/AGENTS.md`): если в проекте есть конфиг линтера, прогон обязателен после каждой серии правок.

- ✅ Правило «проверки после изменений» для `megaapp-back` в `AGENTS.md` и в глобальных инструкциях (Claude и Codex)
- ✅ Линтер в деплой не добавлять

## Часть блока 2 включена (30.10.2026)

Прогон всего блока 2 на коде: 1412 замечаний (revive 666, wrapcheck 287, govet `shadow` 94, gosec 91, perfsprint 73, modernize 59, forcetypeassert 36, unparam 26, contextcheck 18, sloglint 18, nilnil 17, intrange 10, exhaustive 5, gocritic 5, errchkjson 4, prealloc 3; нули: unconvert, usestdlibvars, thelper, tparallel, testifylint, copyloopvar). `gosec` проверен вручную: реальных проблем нет (SQL-срабатывания — ложные, плейсхолдеры строятся из длины; cookie и `math/rand` — осознанно).

Включено: `modernize`, `intrange`, `sloglint`, `gocritic`, `forcetypeassert`, `unparam`. `forcetypeassert` и `unparam` исключены для `_test.go` (тест просто упадёт, тестовые хелперы принимают константы). Не включено: `exhaustive` (5 мест, не выбрано), шесть линтеров с нулём срабатываний (можно включить бесплатно), `revive`, `wrapcheck`, `govet shadow`, `perfsprint`, `nilnil`, `errchkjson`, `prealloc`, `contextcheck`, `gosec`.

Правки (42 файла): `slog.DiscardHandler`; `maps.Copy`, `slices.Sort`/`Contains`, `strings.SplitSeq`, `errors.AsType`, `sync.WaitGroup.Go`, встроенные `min`/`max` (собственные `min`/`max` в `food/service.go` удалены), `new(expr)` вместо тестовых помощников `boolPtr`/`int64Ptr`/`stringPtr`/`float64Ptr`, `for range n`, `reflect.Type.Fields()`. `unparam`: у `parseIDParam` убран параметр имени, у `writeSuccessMessage` — статус, у `parseISODateParts` — неиспользуемый результат дня. `gocritic`: `if-else` → `switch` в `money/service.go`, `switch` с одним case → `if` в `money/repo.go`, `os.Exit` после `defer` в `cmd/server/main.go` убран выносом тела в `run() int`. `forcetypeassert`: значение альфа-канала берётся через `RGBA()` вместо приведения типа.

Итог: `golangci-lint run` 0 замечаний (с тегом `swagger` и без), сборка и тесты (оба варианта) зелёные.

- ✅ Включены `modernize`, `intrange`, `sloglint`, `gocritic`, `forcetypeassert`, `unparam`, все замечания исправлены
- ✅ Решение по `exhaustive` и шести линтерам с нулём срабатываний (см. раздел «Итог по блоку 3» ниже)

## Замер кандидатов блока 3 (30.10.2026)

Замер без включения (временный конфиг), цель — простота, производительность, расширяемость, архитектура.

- `gocognit` (порог 15): 27 в проде. Порог 30 — 6 функций (`Config.Validate` 75, `SaveProduct` 55, `processUserPersonalKcal` 50, `Config.Load` 39, `UpdateTransaction` 32, `buildStatsSummary` 30), порог 40 — 3. `Config.Validate` — плоский список проверок, высокая оценка при низкой реальной сложности.
- `nestif`: 7 в проде, из них 27 (`SaveProduct`) и 12 (`food/service.go:580`) — настоящие очаги.
- `dupl` (порог 100): 17 в проде, все в `money/http.go` (пять почти одинаковых блоков CRUD по 47 строк), `money/service.go`, `money/repo.go`, `food/image_store.go`, `food/personal_kcal_repo.go`. Кандидат на отдельный рефакторинг, не на постоянный гейт.
- `gocritic` (performance): `hugeParam` 55 (80–832 байта, медиана 128), `rangeValCopy` 8. На скорость сервиса влияния нет, правка меняет API на указатели. Не нужен.
- `exhaustive`: все 5 `switch` с `default`, то есть пользы нет (новый элемент enum не пропустится только без `default`). Не включать.
- `gofumpt`: 63 файла переформатируется, пользы для кода нет. Не нужен. `misspell`: 0 срабатываний.
- `deadcode` от `main`: `PersonalKcalResolver.AppliedX` и `App.SQLDB` нигде не вызываются, `App.Serve` — только в тестах. Проект чист.
- Граф импортов: пакеты функциональностей не импортируют друг друга (допустимы `auth`, `ws`, `platform/*`, `httpx/legacy`), `httpx` собирает всё. Нарушений нет, поэтому `depguard` с правилами изоляции сейчас даст 0 срабатываний и станет страховкой. Замечание: все функциональные пакеты зависят от `httpx/legacy` (общие HTTP-хелперы живут внутри корня композиции).

## Итог по блоку 3 (30.10.2026)

Включено: `depguard`, `misspell` и пять линтеров с нулём срабатываний (`unconvert`, `usestdlibvars`, `thelper`, `tparallel`, `copyloopvar`). `testifylint` не включён: `testify` в проекте нет. Удалены две мёртвые функции (`PersonalKcalResolver.AppliedX`, `App.SQLDB`).

Правила `depguard` (в сборке без нарушений, роль — страховка от будущих правок):
- пакеты функциональностей (`backup`, `food`, `ingest`, `metrics`, `money`, `quotes`, `settings`) не импортируют друг друга; общаются через `auth`, `ws`, `platform/*`, `httpx/legacy`, собирает всё `httpx`;
- общие слои (`auth`, `ws`, `config`, `jobs`, `httpx/legacy`, `platform/*`) не импортируют функциональности;
- `platform/*` — нижний слой, не импортирует `auth`, `ws`, `httpx`.
Совпадение пакетов идёт по префиксу, поэтому `metrics` (у него есть подпакет `wire`) вынесен в отдельное правило. Тестовые файлы из правил исключены. Работоспособность правил проверена пробным запрещающим конфигом (срабатывает на существующие импорты).

Сознательно отложено на неопределённый срок (решение пользователя):
- `gocognit` (порог 40) и `nestif` (порог 10) вместе с рефакторингом самых сложных функций: `SaveProduct`, `processUserPersonalKcal`, `Config.Validate`, блок в `food/service.go:580`. Возвращаться только по отдельному решению, с отдельным планом.
- Вынос повторяющегося CRUD в `money` (пять блоков в `money/http.go` и повторы в `money/service.go`, `money/repo.go`, найденные `dupl`) в общий хелпер.
- Вынос `httpx/legacy` из корня композиции в нейтральный пакет.

Сознательно не включаются: `exhaustive`, `gocritic` (performance), `gofumpt`, `dupl` как постоянный гейт.

Итог: `golangci-lint run` — 0 замечаний (с тегом `swagger` и без), сборка и тесты зелёные.

- ✅ Включены `depguard`, `misspell`, `unconvert`, `usestdlibvars`, `thelper`, `tparallel`, `copyloopvar`
- ✅ Удалены мёртвые функции `AppliedX`, `SQLDB`
- ⭕ Отложено на неопределённый срок: `gocognit` + `nestif` с рефакторингом, вынос CRUD в `money`, вынос `httpx/legacy`
