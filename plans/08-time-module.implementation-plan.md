# TIME — Implementation Plan (Backend)

Перенос концепта [`TIME._concept.md`](../../TIME._concept.md) (структура данных уже зафиксирована и не пересматривается) на код `megaapp-back`, по образцу существующего домена `internal/money`. Демо [`TIME.claude-demo.html`](../../TIME.claude-demo.html) — чисто фронтовый прототип, на бэк влияет только косвенно (см. фронтовый план, здесь не обсуждается).

Фронт (см. [`08-time-timeline.implementation-plan.md`](../../megaapp-front/plans/08-time-timeline.implementation-plan.md)) не пагинирует историю — грузит весь датасет пользователя целиком один раз и дальше живёт в `localStorage`, обновляя только небольшой хвост последних дней в фоне. Эндпоинт списка записей ниже спроектирован под эту модель (полная загрузка / обновление хвоста), а не под постраничный infinite-scroll.

Аналитика/статистика в этой итерации **не реализуется** на бэке вообще — см. «Отложено на будущее» в конце; всё по аналитике из концепта (раздел 5) туда и перенесено.

## Как сейчас

- Модуля TIME в бэке нет вообще.
- Структурный эталон — `internal/money`: `types.go` (типы + enum-константы вроде `AccountKind`), `repo.go` (CRUD/запросы), `service.go` (бизнес-инварианты на app-level), `http.go` (`RegisterRoutes` на chi-роутере), `service_test.go`/`http_test.go` (table-driven, temp sqlite, httptest).
- Инварианты в money — не БД constraints (SQLite не умеет exclusion constraints), а сервисный слой: `validateAccountInput`, `ensureAccountReferences`, delete-restrict через подсчёт связанных `moneyTransaction`. Тот же паттерн нужен TIME для overlap-инварианта и restrict удаления Activity.
- Миграции — `migrations/000001`…`000006`, сплошная нумерация, следующая свободная — `000007`. Стиль: `CHECK(...)` на enum-подобных TEXT-полях, `userId` + `FOREIGN KEY ... ON DELETE CASCADE` на каждой таблице, `RESTRICT`/`CASCADE`/`SET NULL` по смыслу связи.
- Модули собираются в `internal/httpx/modules.go` (repo→service→handler), маршруты регистрируются в `internal/httpx/app.go` через `<module>.RegisterRoutes(router, authService, handler)`.

## Как должно быть

### Имя пакета — не `time`

Пакет **нельзя** назвать `internal/time` / `package time` — коллизия со стандартным пакетом `time`, которым бэк пользуется повсеместно (`time.Time`, `time.Now()` и т.д.); пришлось бы алиасить stdlib почти в каждом файле пакета. Пакет называется `internal/timetrack` (`package timetrack`). На таблицы БД и HTTP-префикс это не влияет — там это просто строки, остаются как в концепте (`timeActivity`, `timeCategory`, `timeActivityCategory`, `timeEntry`, префикс `/api/time`).

Файлы пакета — один в один структура money: `types.go`, `repo.go`, `service.go`, `http.go`, `service_test.go`, `http_test.go`.

### Миграция — `migrations/000007_time_schema.sql`

Четыре таблицы один в один по разделу 6 концепта: `timeActivity`, `timeCategory`, `timeActivityCategory`, `timeEntry`. `userId` + `FOREIGN KEY ... ON DELETE CASCADE` на каждой; `timeEntry.activityId` — `ON DELETE RESTRICT`; `timeActivityCategory` — обе FK `ON DELETE CASCADE`; индекс `(userId, track, startAt)` на `timeEntry`.

Enum-подобные поля: `track` и `impact` — закрытый список значений, годятся под `CHECK(...)` в самой миграции (как `symbolPosEnum`/`categoryType` в money). `timeCategory.kind` — nullable и намеренно открытый (концепт: `'area'` сейчас, но допускает произвольные будущие значения для описательных тегов) — `CHECK` тут смысла не имеет за пределами защиты от опечатки в `'area'`, остальная валидация — app-level.

Это единственная миграция TIME, пока фича не зарелизена — правки схемы вносятся в этот же файл на месте (правило из `~/.claude/rules/database.md`), новая миграция — только после релиза.

### Доменная модель (`types.go`)

- `Track` — строковый тип + константы `Primary`/`Secondary` (по образцу `money.AccountKind`).
- `Impact` — константы `Useful`/`Neutral`/`Wasteful`.
- `CategoryKind` — nullable, константа `Area`, остальные значения не перечисляются закрытым списком (открытый на будущее, как решено в концепте).
- Структуры `Activity`, `Category`, `Entry` — поля как в разделе 6 концепта; Input DTO на создание/обновление Activity/Category/Entry, по аналогии `AccountInput`/`TransactionInput`.

### Репозиторий (`repo.go`)

- CRUD Activity/Category.
- Замена полного набора `categoryIds` у Activity одним вызовом (delete+insert в транзакции, не diff) — простейший вариант, категорий на активность немного.
- Entry: CRUD; выборка списка с опциональным `start` (записи с `startAt >= start`, без верхней границы) — при отсутствии `start` отдаёт всю историю пользователя целиком (первый визит без `localStorage`), при `start = today-N` отдаёт только свежий хвост (фоновое обновление кэша); это не постраничный курсор, вызывается ровно этими двумя сценариями. Отдельно — выборка пересекающихся записей того же `(userId, track)` для overlap-инварианта (свой id исключается при update).
- "Последние использованные" активности — `MAX(startAt) GROUP BY activityId`, опциональный фильтр по track, без отдельной таблицы (зафиксировано в концепте, раздел 3/71) — для автокомплита/quick-picker на фронте.

### Сервис (`service.go`) — инварианты, app-level

- `endAt > startAt`.
- Нет пересечения с другим Entry того же `userId`+`track` (между треками — не проверяется вообще). Эта же проверка — единственная защита от гонки при параллельных правках с фронта (см. фронтовый план, раздел «Синхронизация с сервером»): фронт намеренно не сериализует запросы в очередь, полагаясь на то, что бэк отклонит конфликтующий по времени запрос ошибкой, а не молча испортит данные.
- **Проверка пересечения + запись — под одним `sync.Mutex` внутри пакета `timetrack`, ничего в общей инфраструктуре БД не меняется.** Без этого возможен TOCTOU: запрос A читает "пересечений нет", запрос B параллельно читает то же самое (A ещё не записал), затем оба пишут — оба проходят, пересечение всё равно образуется. Решение полностью локально: один `sync.Mutex` (или `sync.RWMutex`) как поле `Service`/`Repository` в `internal/timetrack`, `Lock()` в начале create/update Entry, `defer Unlock()`. На время операции все остальные create/update Entry (от любого пользователя) ждут своей очереди — для личного проекта это не проблема производительности, а для одного пользователя фактически всегда так и есть. Сама запись при этом всё равно оборачивается обычной `db.BeginTx(ctx, nil)` (как `money.CreateTransferPair`, `repo.go:618`) — это только про атомарность (откат при ошибке), не про блокировку от гонки, ту делает мьютекс.
- Ничего в `internal/platform/sqlite/db.go` (пул соединений, DSN) этим планом не меняется — решение целиком внутри нового пакета, money/food/metrics не затронуты никак.
- `track`/`impact` — валидация допустимых значений.
- Удаление Activity запрещено при наличии ссылающихся Entry (RESTRICT + ошибка `conflict`, аналог account/transaction restrict в money) — только `isArchived`. Category удаляется свободно (Entry на неё не ссылается, только cascade по join-таблице).
- Создание/обновление Activity с одновременной заменой `categoryIds` — одной сервисной операцией (транзакция).

### HTTP (`http.go`) — маршруты `/api/time/*`

- Activities: список (с фильтром `isArchived`), создание, обновление (включая `isArchived`-toggle и `categoryIds`); без DELETE.
- Categories: список, создание, обновление, удаление.
- Entries: список, опциональный query `start` (см. репозиторий выше — полная история без параметра, хвост последних дней с параметром); создание; обновление (это же move/resize/смена activity или track — один и тот же PUT с новыми `startAt`/`endAt`/`activityId`/`track`); удаление.
- Recent activities: список последних использованных `activityId`, опционально фильтр по track.

### Wiring

- `internal/httpx/modules.go`: `timetrackModule{service, handler}`, `buildTimetrackModule(db)` — repo→service→handler, один в один `buildMoneyModule`.
- `internal/httpx/app.go`: `timetrack.RegisterRoutes(router, authModule.service, timetrackModule.handler)`.

### Тесты

- `service_test.go` — table-driven, temp/in-memory sqlite (`openTimetrackTestDB` по образцу `openMoneyTestDB`), фикстуры `insertTimetrack*`; покрытие: overlap внутри трека отклоняется / между треками — нет, `endAt>startAt`, restrict удаления Activity при наличии Entry, атомарная замена `categoryIds`, "последние использованные".
- `http_test.go` — `httptest.Server` + chi, аналог `assertMoneyStatus`; CRUD activities/categories/entries + список с `start`/без него.

## Что нужно сделать (изменения в существующем коде)

- Новый файл `migrations/000007_time_schema.sql`.
- Новый пакет `internal/timetrack/{types,repo,service,http,service_test,http_test}.go`.
- `internal/httpx/modules.go` — добавить `timetrackModule` + `buildTimetrackModule`.
- `internal/httpx/app.go` — зарегистрировать `timetrack.RegisterRoutes`.
- `money`/`food`/`metrics` не трогаются — TIME полностью изолирован, как остальные домены.

## To-do

- ✅ `migrations/000007_time_schema.sql` — 4 таблицы, FK, индекс `(userId, track, startAt)`
- ✅ `internal/timetrack/types.go` — `Track`/`Impact`/`CategoryKind` константы, структуры Activity/Category/Entry, Input DTO
- ✅ `internal/timetrack/repo.go` — CRUD activities/categories, замена `categoryIds`, entry CRUD, список entries (полный/с `start`), пересекающиеся entries (overlap), recent-activities
- ✅ `internal/timetrack/service.go` — `sync.Mutex` вокруг create/update Entry (проверка пересечения + запись), `BeginTx` для атомарности самой записи — оба локальны для пакета, ничего вне `internal/timetrack` не меняется
- ✅ `internal/timetrack/service.go` — все app-level инварианты, restrict удаления Activity
- ✅ `internal/timetrack/http.go` — `RegisterRoutes`, все эндпоинты (activities/categories/entries/recent)
- ✅ `internal/httpx/modules.go` — `buildTimetrackModule`
- ✅ `internal/httpx/app.go` — регистрация роутов
- ✅ `internal/timetrack/service_test.go` — все инварианты, table-driven, включая тест на гонку (два конкурентных create с пересечением — ровно один должен пройти)
- ✅ `internal/timetrack/http_test.go` — CRUD + список (полный/с `start`) через httptest
- ✅ `migrations/000007_time_schema.sql` — добавлен `settings.selectedChapterTime` (тумблер раздела в настройках), правка внесена в тот же файл, фича ещё не зарелизена
- ✅ `internal/settings/*` — `selectedChapterTime` проведён через repo/service/http (types, UpdateField, ParseUpdateInput) по аналогии с `selectedChapterFood`/`selectedChapterMoney`

## Отложено на будущее (не в этой итерации)

- Аналитика/статистика (концепт, раздел 5): агрегирующие запросы по периоду с усечением на границах (пропорциональный учёт при пересечении полуночи/конца месяца/года), группировки по `timeCategory.kind='area'`/конкретной `timeCategory`/`Activity`, вес трека (primary/secondary) как параметр запроса. GET-эндпоинт(ы) `/api/time/stats`. Без кэш-таблицы на старте (прецедент — `internal/food/stats_cache.go`, in-memory), кэш — только если станет медленно на реальном объёме.
