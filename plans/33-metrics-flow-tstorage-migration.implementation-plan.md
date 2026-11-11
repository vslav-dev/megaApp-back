# 33. Metrics flow: миграция на бинарный wire-формат — megaapp-back

Часть кросс-репо миграции metrics-flow на tstorage-архитектуру, описанной в [flatline:07-research](../../../flatline/plans/07-metrics-flow-performance.refactoring-research.md) (§5 приложения) и реализуемой в [flatline:07-implementation-plan](../../../flatline/plans/07-metrics-flow-tstorage-migration.implementation-plan.md), который фиксирует контракт, используемый здесь. Симметричный план фронта — [front:33](../../megaapp-front/plans/33-metrics-flow-tstorage-migration.implementation-plan.md).

## 0. Рамки

- Meняется только **read-путь** (`/api/metrics/history`, `/api/metrics/since`, WS `METRICS_UPDATE`/`METRICS_LATEST`) — переход JSON → бинарный формат Flatline (flatline:07 §4).
- **Не меняется**: собственный ingest мегабэка в Flatline (`Exporter`/`outbox.go`, self-metrics push) — работает по замороженному JSON-контракту (flatline:07 §3), не трогаем вообще.
- **Не меняется**: скоуп-логика (`scope.go`, subscribe/unsubscribe семантика) — уже эффективна, вопрос формата payload'а её не касается.
- **Не участвует в решении о глубине истории**: сколько точек по каждой гранулярности запрашивать (в частности, новый потолок фронта — 24ч/1440 точек по minute вместо прежних 48ч, см. [front:33 §2.1](../../megaapp-front/plans/33-metrics-flow-tstorage-migration.implementation-plan.md)) решает фронт через floor-параметры запроса; back как был чистым передатчиком запроса, так и остаётся — глубину не ограничивает и не знает о ней.
- **Не в объёме**: A1 (разделение таймаутов `HTTPWriteTimeout`, research-док §2.1/§3-A1) — отдельный маленький срочный фикс, независимый от этой миграции, рекомендуется сделать до или параллельно, не как часть этого чеклиста.
- **Не в объёме**: `PERFORMANCE_METRICS_BATCH`/NDJSON-канал (`internal/ws/performance_metrics.go`) — отдельная, уже описанная проблема (research-док A4), не трогаем.

## 1. Текущая реализация (для справки, детали — см. предыдущий код-дайв этой сессии)

- `internal/metrics/flatline_client.go`: `FlatlineClient.History`/`Since` — JSON-декод ответа Flatline в Go-структуры.
- `internal/metrics/http.go`: `HistoryHandler.History` — JSON-декод запроса от фронта, вызов клиента, JSON-энкод ответа обратно во фронт (два независимых JSON-прохода на один запрос).
- `internal/metrics/poller.go`: `Poller.tick` — раз в `FlatlinePollInterval` (10с) дергает `client.Since`, дедупит по `seenBucket`, обновляет `latest`/`lastBucket`.
- `internal/metrics/realtime.go`: `BroadcastDetail`/`BroadcastLatest` — заворачивают точки в `map[string]any{"type":..., "payload":...}`, JSON-энкодятся `ws.Client.SendJSON` на каждого подписанного клиента.

## 2. Целевая архитектура

### 2.1. `/api/metrics/history` — потоковый passthrough, без декода

Сегодня back — «тонкий, но не байтовый» прокси: делает `POST` к Flatline, полностью JSON-декодирует ответ в `[]ServiceHistory`, затем заново JSON-энкодит его во фронт (research-док §1.3 п.2). При бинарном формате декодировать нечего — back не читает содержимое ответа Flatline вообще, только копирует байты.

`HistoryHandler.History`: после auth/admin-проверок (не меняются) и валидации запроса (не меняется) — вместо `client.History(...) → []ServiceHistory → writeJSON` делает один `POST` к Flatline и `io.Copy` тела ответа Flatline напрямую в свой `ResponseWriter` (с проброшенным `Content-Type`/`Content-Length` от Flatline). `FlatlineClient.History` меняет сигнатуру: вместо декодированной структуры возвращает `*http.Response` (или сразу пишет в переданный `io.Writer` — решение уровня кода) — не декодирует тело.

Это не отменяет A1 (§0) — сколь угодно быстрый passthrough не меняет то, что серверный `WriteTimeout` и клиентский таймаут к Flatline остаются двумя гонящимися друг за другом бюджетами; A1 остаётся отдельной обязательной задачей.

### 2.2. `Poller`/`Since` — реальный декод остаётся, но дешевле

В отличие от `/history`, `Poller` **обязан** разбирать точки (дедуп по `seenBucket`, обновление `latest`/`lastBucket`) — здесь passthrough невозможен. `FlatlineClient.Since` меняется с JSON-декода на декод бинарного формата (flatline:07 §4) в те же Go-структуры `MetricPoint`, что и сегодня — дешевле, чем `encoding/json`-рефлексия, но по-прежнему настоящий decode-шаг. Вся логика `tick()` (catch-up floor, дедуп по `seenBucket`, обновление `latest`/`lastBucket`, вызовы `BroadcastDetail`/`BroadcastLatest`) не меняется — меняется только то, чем `client.Since` наполняет `[]MetricPoint`.

### 2.3. WS-рассылка (`METRICS_UPDATE`/`METRICS_LATEST`) — новый энкод на границе back→front

Это тот самый «один маленький неизбежный переход», о котором говорит research-док §5 приложения: back знает только что реально изменилось (после дедупа и скоуп-фильтрации per-client — `filterPointsByScope`, не меняется), и должен упаковать именно это на каждый WS-тик. JSON-энкод (`SendJSON`) меняется на энкод в тот же бинарный формат, что использует Flatline на `/history`/`/since` (переиспользуем один и тот же формат/энкодер на обеих границах — не изобретаем второй).

**Решение по фреймингу** (не JSON с вложенным бинарником — это бы заставило бинарные байты пройти через base64 внутри JSON-строки, что как раз и убивает весь смысл перехода на бинарный формат): `METRICS_UPDATE`/`METRICS_LATEST` уходят как **нативные бинарные WS-фреймы** (WebSocket и так поддерживает два типа фрейма — текстовый и бинарный, это не что-то экзотическое, `ws.Client` получает новый метод `SendBinary` рядом с уже существующим `SendJSON`). Различение `METRICS_UPDATE` от `METRICS_LATEST` — один байт-префикс перед данными, не JSON-поле. `METRICS_SUBSCRIBE`/`METRICS_UNSUBSCRIBE` (входящие, от фронта) остаются как есть, обычным JSON-текстовым фреймом — они редкие и маленькие, менять их не нужно.

`Realtime.BroadcastDetail`/`BroadcastLatest`, `filterPointsByScope`, `subscriberSnapshot` (копия под локом перед сетевым I/O) — логика не меняется, меняется только последний шаг (что именно уходит в сокет).

### 2.4. Общий энкодер/декодер — переиспользуемый пакет

Раз бинарный формат используется в трёх местах (`FlatlineClient.Since`-декод, `HistoryHandler` passthrough без декода, `Realtime`-энкод для WS) — выносится в один внутренний пакет (например, `internal/metrics/wire`), общий для чтения ответа Flatline и записи в WS. Не дублируется отдельно под каждый вызов.

## 3. Что не меняется — явно

- `internal/metrics/scope.go` — без изменений.
- `internal/metrics/ws_handlers.go` (`METRICS_SUBSCRIBE`/`METRICS_UNSUBSCRIBE`) — без изменений, работают со скоупом, не с форматом точек.
- `internal/metrics/exporter.go`, `outbox.go` — без изменений (ingest-контракт заморожен).
- `internal/metrics/process_sampler.go` — без изменений (производитель, не потребитель).
- `internal/config/config.go`, `internal/httpx/app.go`/`modules.go` — без изменений в рамках этого плана (кроме A1, который отдельно).

## 4. Чеклист

- ✅ Определить общий бинарный формат совместно с flatline:07 (единый на `/history`, `/since`, WS) — вынесен в `internal/metrics/wire` (байт-в-байт копия flatline'ского пакета — репы разные модули, код не шарится, см. §2.4)
- ✅ `FlatlineClient.History` → потоковый passthrough: возвращает сырой `*http.Response`, вызывающий код копирует тело и не декодирует его
- ✅ `HistoryHandler.History` → отдаёт тело Flatline как есть (`io.Copy`, проброшены `Content-Type`/`Content-Length`), статус-коды/обработка ошибок (502 на сбой похода к Flatline) не изменились
- ✅ `FlatlineClient.Since` → декод бинарного формата (`wire.Decode`) вместо JSON
- ✅ `Realtime.BroadcastDetail`/`BroadcastLatest` → энкод в бинарный формат вместо `SendJSON`(JSON); `BroadcastLatest` пакует каждую (service, metricName) в одноточечную `wire.Series` (granularity не имеет смысла для "последнего значения" — фиксированный placeholder minute)
- ✅ `ws.Client.SendBinary` + `Hub.BroadcastBinaryToUser` — нативный бинарный WS-фрейм с байт-префиксом типа (`METRICS_UPDATE`=0/`METRICS_LATEST`=1), без JSON-обёртки/base64
- ✅ Прогнан существующий тест-сьют (`flatline_client_test.go`, `poller_test.go`, `realtime_test.go`) — адаптирован под новый формат, поведенческие гарантии (дедуп, catch-up floor, scope-фильтрация, resubscribe-replace) сохранены; `go build`/`go vet`/`go test -race ./...` — зелёные
- ⭕ Скоординировать атомарный релиз с [flatline:07](../../../flatline/plans/07-metrics-flow-tstorage-migration.implementation-plan.md) и [front:33](../../megaapp-front/plans/33-metrics-flow-tstorage-migration.implementation-plan.md)
- ⭕ (отдельно, не блокирует эту миграцию) A1 — развести серверный `WriteTimeout` и клиентский таймаут к Flatline, см. research-док §2.1
