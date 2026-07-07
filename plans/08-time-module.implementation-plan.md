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

- Аналитика/статистика (концепт, раздел 5): агрегирующие запросы по периоду с усечением на границах (пропорциональный учёт при пересечении полуночи/конца месяца/года), группировки по `CategoryGroup.kind='area'`, любой другой `CategoryGroup + CategoryOption`, `ActivityKind`, вес трека (primary/secondary) как параметр запроса. GET-эндпоинт(ы) `/api/time/stats`. Без кэш-таблицы на старте (прецедент — `internal/food/stats_cache.go`, in-memory), кэш — только если станет медленно на реальном объёме.

## Новый этап: backend для entry-level категоризации

Этот план выше описывает **закрытый этап v1**:
- интервалы;
- track;
- Activity;
- Activity-level Category;
- CRUD и доска ввода.

Он не переписывается и остаётся историей первого завершённого этапа.

Следующий этап начинается поверх него и меняет не механику времени, а **семантическую модель выбора деятельности внутри записи**.

### Что уже решено концептуально

- `primary` / `secondary` сохраняются.
- Интервальная модель сохраняется.
- Плоская связка `timeActivity + timeCategory + timeActivityCategory` больше не считается конечной.
- Новый фокус — **крупные виды активности** + **группы значений** + **выбор значений в конкретной записи**.

### Что это означает для бэка

В следующей итерации бэку понадобится новый слой reference-данных:
- сущность крупного вида активности (`Игра`, `Видео`, `Код`, `Еда`, `Проезд`, ...)
- сущность группы категорий / аналитического направления (`Развлечение / Работа / Обучение / ...`, `Игра`, `Источник видео`, `Режим кода`, `На кого`, ...)
- сущность значения внутри группы (`ATS`, `YouTube`, `Развлечение`, `Обучение`, ...)
- связь применимости: какие группы разрешены/обязательны для какого вида активности

Для вида активности должно храниться не только "какие группы допустимы", но и:
- какие из них обязательны;
- какие опциональны.

Ключевой принцип модели:
- любая группа может быть привязана к любой активности;
- каждая такая привязка создаётся только вручную;
- у бэка не должно быть механики "эта группа по умолчанию общая для всех" или "эта группа автоматически применяется ко всем активностям";
- `required` остаётся частью модели.

Предвыбор значений не хранится в reference-структуре.
- Он относится к фронтовому runtime-слою создания записи.
- Исторические данные для такого предвыбора бэк только хранит и отдаёт через обычные факты записей.

И отдельный слой выбора внутри записи:
- запись должна хранить не только "какая Activity выбрана", но и "какие значения групп выбраны для этого конкретного вхождения"
- при двух треках это должно быть привязано к конкретному activity-instance, а не просто к entry целиком, иначе значения двух параллельных активностей смешаются

### Что останется без изменений

- таблица фактов времени как интервал
- overlap-check
- `track`
- механика списка записей и обновления хвоста
- optimistic sync-контракт фронта: бэк по-прежнему валидирует итоговое состояние

### Что, вероятно, придётся переделать в API

- `GET /api/time/activities` перестанет быть единственным справочником выбора
- появятся новые read-endpoints для:
  - видов активности
  - групп
  - значений
  - связей применимости
- payload `POST/PUT /api/time/entries` перестанет быть просто `{ activityId, track, startAt, endAt }`
- в запись нужно будет передавать структурированное описание выбранного activity-instance

Сервер должен уметь валидировать:
- что обязательные группы заполнены;
- что для данного activity-kind нельзя передать постороннюю группу;
- что выбранное значение принадлежит правильной группе;
- что в запись не попадают искусственные значения, придуманные вне справочников;
- что формат хранения не смешивает две параллельные activity-instance внутри одного entry.

### Что важно не сломать

- возможность частичной разметки: не все activity family обязаны иметь одинаковую глубину параметров
- дальнейшую аналитику надо строить по группам/значениям, не теряя старый срез по самим activity/видам
- ручная привязка group к activity должна оставаться единственным источником применимости
- предвыбор значений не должен просачиваться в schema/reference-модель как отдельный structural-layer
- не делать никаких fallback-слоёв ради совместимости со старым v1-кодом: текущая фича ещё не закончена, поэтому новый этап можно и нужно проектировать как чистую целевую модель, а не как аккуратную надстройку поверх старых решений

### Принцип реализации этого плана

- Пока не завершён весь второй этап TIME, все schema-правки вносятся в **одну и ту же последнюю миграцию** этого плана.
- Не плодить несколько миграций под один и тот же ещё незавершённый redesign.
- Не сохранять старую структуру кода только потому, что она уже написана.
- Качество итоговой архитектуры важнее сохранности текущей реализации.
- Если для упрощения и очистки архитектуры потребуется выбросить значительную часть текущего TIME-кода и собрать заново более прямую модель, это и считается правильным действием.

### Грубый backend scope следующей итерации

- новая схема reference-данных вместо/поверх текущего плоского `timeCategory.kind`
- новая схема выбора значений внутри записи
- новые DTO и валидация для structured entry input
- новые repo/service/http слои для чтения справочников нового формата
- миграционный план импорта старых Activity/Category в новую модель
- серверная модель applicability/required на уровне activity-kind × group
- без глобальных group-templates и без auto-apply логики
- аналитика и справочники должны опираться именно на группы и значения, без лишних служебных сущностей поверх этого

### Что пока не зафиксировано

- будет ли `timeActivity` переосмыслена как `ActivityKind` или появится новая таблица
- точные имена новых таблиц и DTO
- materialize ли предвыбранное значение в запись всегда явно или разрешить пустое поле до явного подтверждения фронтом

## Новая архитектура второго этапа: backend

Этот блок описывает только целевую модель. Как перейти к ней из текущего v1-кода — в следующем блоке.

### Масштаб

TIME — личный модуль с малым объёмом данных и без высокой нагрузки; поэтому простая app-level модель важнее полной SQL-нормализации.

### Главный принцип

Одна строка `timeEntry` = один конкретный activity-instance:
- свой `track`;
- свой интервал;
- свой `activityKindId`;
- свой набор выбранных group options.

Отдельная таблица activity-instance не нужна. Primary и secondary не смешиваются, потому что это разные `timeEntry`.

### Схема

Целевая схема состоит из 5 таблиц:
- `timeActivityKind`
  - крупный вид активности: `Игра`, `Видео`, `Код`, `Еда`, `Проезд`
  - поля: `id`, `userId`, `name`, `groupBindingsJson`, `isArchived`, `createdAt`, `updatedAt`
  - `groupBindingsJson`: список вручную привязанных групп + `required`
- `timeCategoryGroup`
  - аналитическое направление: `Игра`, `Источник видео`, `Режим кода`, `На кого`, `Область`
  - поля: `id`, `userId`, `name`, `kind`, `isArchived`, `createdAt`, `updatedAt`
  - `kind`: nullable, сейчас только `area` для основной группы аналитики/цвета
- `timeCategoryOption`
  - значение внутри группы: `ATS`, `YouTube`, `Работа`, `Обучение`
  - поля: `id`, `userId`, `groupId`, `name`, `color`, `isArchived`, `createdAt`, `updatedAt`
- `timeEntry`
  - факт времени
  - поля: `id`, `userId`, `activityKindId`, `track`, `startAt`, `endAt`, `createdAt`, `updatedAt`
  - индекс: `(userId, track, startAt)`
- `timeEntryOption`
  - выбранное значение группы внутри конкретной записи
  - поля: `entryId`, `groupId`, `optionId`
  - `PRIMARY KEY (entryId, groupId)`

DB-level FK:
- `timeCategoryOption.groupId`;
- `timeEntry.activityKindId`.
- `timeEntryOption.entryId`;
- `timeEntryOption.groupId`;
- `timeEntryOption.optionId`.

Ids внутри `groupBindingsJson` валидируются в service перед записью. Выбранные options вынесены в таблицу, потому что это факты для будущей аналитики.

### Инварианты

- `endAt > startAt`.
- Нет пересечений внутри `(userId, track)`.
- `activityKindId` принадлежит тому же `userId`.
- Все required-группы выбранного kind заполнены.
- Optional-группы могут отсутствовать.
- Каждая переданная группа есть в `groupBindingsJson` выбранного kind.
- Каждый `optionId` принадлежит своему `groupId`.
- Внутри одной группы у одной записи может быть только один option.
- Нельзя передать чужие ids.
- Нельзя создать entry с archived kind или archived option.
- Нельзя создать/обновить entry с archived group.
- CategoryGroup с активными bindings в неархивных activity kinds нельзя архивировать.
- Для `kind='area'` допускается максимум одна неархивная group на пользователя.
- Existing entries остаются историческими фактами после архивации справочника.

### Операции

Create entry:
- проверить interval + overlap;
- проверить `activityKindId`;
- распарсить `groupBindingsJson`;
- проверить required-группы;
- проверить каждую пару `groupId + optionId`;
- записать `timeEntry`;
- записать `timeEntryOption`.

Update time:
- меняет только `track`, `startAt`, `endAt`;
- не перевалидирует semantic selection;
- используется для drag/resize/move.

Update selection:
- меняет `activityKindId` и полный набор `timeEntryOption`;
- валидирует selection как create entry;
- обновляет `timeEntry.activityKindId`;
- заменяет options через delete+insert в одной транзакции.

### API

Read:
- `GET /api/time/catalog` — kinds, groups, options, group bindings.
- `GET /api/time/entries` — вся история.
- `GET /api/time/entries?start=...` — свежий хвост.

Reference writes:
- `POST /api/time/activity-kinds`
- `PUT /api/time/activity-kinds/{id}`
- `POST /api/time/category-groups`
- `PUT /api/time/category-groups/{id}`
- `POST /api/time/category-options`
- `PUT /api/time/category-options/{id}`

Entry writes:
- `POST /api/time/entries`
- `PATCH /api/time/entries/{id}/time`
- `PATCH /api/time/entries/{id}/selection`
- `DELETE /api/time/entries/{id}`

### Аналитика

Будущая аналитика читает:
- длительность/период/track из `timeEntry`;
- вид активности из `timeEntry.activityKindId`;
- группы/значения из `timeEntryOption + timeCategoryOption + timeCategoryGroup`.

Rollup-таблицы не нужны до реального профилирования.

## Переход текущей backend-реализации к новой архитектуре

Этот блок — карта переделки v1-кода. Он не описывает новую архитектуру заново.

### Schema migration

- Переписать `migrations/000007_time_schema.sql`, потому что TIME ещё не зарелизен.
- Не добавлять новую миграцию под этот redesign.
- Заменить `timeActivity` на `timeActivityKind`.
- Заменить `timeCategory` на пару `timeCategoryGroup` + `timeCategoryOption`.
- Удалить `timeActivityCategory`.
- В `timeEntry` заменить `activityId` на `activityKindId`.
- Добавить `timeEntryOption`.
- Сохранить индекс `(userId, track, startAt)`.
- V1-данные и TIME localStorage не сохранять: данных нет, схему можно пересоздать.

### Domain types

- `Activity` заменить на `ActivityKind`.
- `Category` заменить на `CategoryGroup` и `CategoryOption`.
- В `CategoryGroup` добавить nullable `kind`; сейчас поддерживается только `area`.
- `Entry.activityId` заменить на `Entry.activityKindId`.
- В `Entry` добавить `Options []EntryOption`.
- `EntryOption` — `groupId + optionId`, хранится в `timeEntryOption`.
- В `ActivityKind` добавить `GroupBindings []GroupBinding`.
- `GroupBinding` — DTO/value type: `groupId + required`.
- JSON marshal/unmarshal нужен только для `GroupBindings`; наружу отдавать обычный array.

### Repository

- Catalog read должен отдавать kinds, groups, options одним набором.
- Activity CRUD заменить на activity-kind CRUD.
- Category CRUD разделить на group CRUD и option CRUD.
- Category option всегда проверяет `groupId`.
- Entry read должен подтягивать options из `timeEntryOption`.
- Entry create/update-selection должен заменять `timeEntryOption` через delete+insert.
- Убрать операции замены `categoryIds` у Activity.
- Убрать queries по `timeActivityCategory`.

### Service

- Сохранить overlap-lock вокруг create/update-time.
- Разделить entry methods:
  - create entry;
  - update entry time;
  - update entry selection;
  - delete entry.
- Create/update-selection валидируют group bindings, required, group kind, single-select, option ownership.
- Update-time валидирует только interval/track/overlap.
- Group archive блокируется, если group есть в bindings неархивного kind.
- Option/kind archive скрывает справочник от новых записей, но не ломает existing entries.
- Reference delete: hard delete только для неиспользованных rows, иначе `isArchived`.

### HTTP

- `GET /api/time/activities` заменить на `GET /api/time/catalog`.
- Activity endpoints заменить на activity-kind endpoints.
- Category endpoints заменить на group/option endpoints.
- `POST /api/time/entries` принимает `activityKindId + options`.
- `PATCH /api/time/entries/{id}/time` принимает только time-поля.
- `PATCH /api/time/entries/{id}/selection` принимает только semantic selection.
- Если временно остаётся `PUT /api/time/entries/{id}`, handler всё равно должен вызвать отдельный service method по смыслу изменения.

### Tests

- Обновить migration/test DB fixture под 5 таблиц.
- Переписать service tests на kind/group/option/timeEntryOption.
- Покрыть required-группы.
- Покрыть forbidden group for kind.
- Покрыть option from wrong group.
- Покрыть single-select: два option одной group в одной entry запрещены.
- Покрыть one active `area` group per user.
- Покрыть запрет архивации group, если она есть в bindings неархивного kind.
- Покрыть archived kind/option.
- Сохранить tests на overlap внутри track и разрешённый overlap между tracks.
- Покрыть update-time без перевалидации selection.
- Покрыть update-selection без изменения time.

### Backend to-do второго этапа

- ✅ Переписать `000007_time_schema.sql` под 5 целевых таблиц.
- ✅ Заменить `Activity`/`Category` domain model на `ActivityKind`/`CategoryGroup`/`CategoryOption`.
- ✅ Добавить `CategoryGroup.kind` с поддержкой `area`.
- ✅ Провести `groupBindingsJson` через repo/service/http как обычный `GroupBinding[]` DTO.
- ✅ Провести `timeEntryOption` через repo/service/http как обычный `EntryOption[]` DTO.
- ✅ Переписать catalog repository/API на единый `/api/time/catalog`.
- ✅ Разделить entry service/API на create, update time, update selection, delete.
- ✅ Добавить validation group bindings / required / group kind / single-select / option ownership.
- ✅ Добавить правила архивации group/kind/option.
- ✅ Убрать v1 categoryIds/activity-category код.
- ✅ Обновить service/http tests под новую модель.

### Отклонения от плана при реализации

- API-контракт в разделе «API» не перечислял `DELETE` для `category-groups`/`category-options`, хотя раздел «Инварианты» требует «hard delete только для неиспользованных rows». Добавлены `DELETE /api/time/category-groups/{id}` и `DELETE /api/time/category-options/{id}` (по образцу v1 `DELETE /categories/{id}`); `activity-kinds` без DELETE, как и v1 `Activity` (только `isArchived`-toggle через PUT).
- `GET /api/time/activities/recent` (v1) не перенесён в v2 и удалён вместе с `ListRecentActivityIDs`/`GetRecentActivityIDs` — v2 API-контракт его не содержит, а фронтовый picker в v2 считает частоту использования сам из уже загруженного полного `entries$$` (`usageStatsByKindAndGroup`), эндпоинт стал мёртвым кодом.
- `PUT /api/time/entries/{id}` (v1) не сохранён даже как временный алиас — сразу только `PATCH .../time` и `PATCH .../selection`, согласно «не делать fallback-слоёв ради совместимости».
- ON DELETE для новых FK решены явно (в плане не были прописаны): `timeEntryOption.entryId` → `CASCADE`; `timeEntryOption.{groupId,optionId}` и `timeCategoryOption.groupId` → `RESTRICT` (симметрично `timeEntry.activityKindId`), с app-level pre-check по паттерну `money.ensureAccountReferences` (COUNT перед удалением/архивацией, не перехват сырой FK-ошибки).
- `CategoryGroup.kind` на уровне схемы получил `CHECK(kind IS NULL OR kind = 'area')` — в отличие от v1 `timeCategory.kind`, который был открытым текстовым полем без CHECK. Решение: план явно фиксирует «сейчас поддерживается только area», в отличие от v1, где `kind` был намеренно свободным полем.
