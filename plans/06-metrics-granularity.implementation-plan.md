# Metrics Granularity — Implementation Plan (Backend)

Часть единого плана [`METRICS-GRANULARITY._implementation-plan.md`](../../../METRICS-GRANULARITY._implementation-plan.md) в корне `code/` — туда читать общую архитектуру, почему агрегация считается в Flatline, и почему курсор сравнивается с `bucket + step`, а не просто с `bucket`. Предполагает, что Flatline уже обновлён по [`flatline/plans/02-metrics-granularity.implementation-plan.md`](/Users/user/code/flatline/plans/02-metrics-granularity.implementation-plan.md).

> **Ревизия 1:** предыдущая версия заводила здесь новую таблицу `metric_rollups` и движок агрегации. Отклонено — агрегация переехала в Flatline целиком (одна копия данных и логики, не две).
>
> **Ревизия 2:** следующая версия предлагала, что `Poller` опрашивает Flatline 3 раза за тик (по курсору на каждую гранулярность), затем — что курсор переходит на `id` (порядок вставки строки). Оба варианта отклонены как избыточные/хрупкие. Финально: Flatline сам учитывает длину периода в сравнении (`bucket + step > cursor`), курсор остаётся обычной временной меткой, megaapp-back вообще не меняет курсорную механику `Poller` — она и так уже делает то, что нужно.
>
> **Ревизия 3 (пост-релиз, баг):** клиентский `cursor` в `METRICS_SUBSCRIBE`, оставленный только для `minute` (см. ниже), оказался единственной хрупкой точкой системы — рассинхрон фронтового курсора (гонка с асинхронной загрузкой IndexedDB, очистка кэша посреди работы) отфильтровывал легитимный бэкфилл как «уже виденное». Усугублялось тем, что ошибка `METRICS_SUBSCRIBE`-хендлера (например, таймаут похода в Flatline) молча проглатывалась в `internal/ws/hub.go:readLoop` — бэкфилл не приходил вообще, без единого лога. Итог: удалён клиентский cursor для `minute` тоже — теперь все три гранулярности живут по одному принципу (фиксированное окно релея, никакого клиентского состояния), плюс хендлер логирует свои ошибки через `slog`.
>
> **Ревизия 4 (пост-релиз, найдено логами после Ревизии 3):** логирование из Ревизии 3 тут же поймало реальную причину пустого бэкфилла — `Since(ctx, 0)` тянул у Flatline вообще всё хранимое (минутки за 30 суток ретеншна по всем сервисам — на проде это оказалось 581k строк / ~70 МБ) и резал лишнее уже в Go (`filterPointsForRelay`). На 1-ядерном/~1ГБ VPS такой ответ не укладывался в 5-секундный HTTP-таймаут клиента (общий с пуш-таймаутом) — бэкфилл валился по таймауту на каждой попытке. Исправлено по обе стороны: Flatline (`ListSince`/`/api/metrics/since`) принимает дополнительные `minuteSince`/`hourSince`/`daySince` — per-granularity нижние границы, применяются в SQL по индексу, а не после в Go. Все четыре параметра (`cursor` + три floor) **обязательны** — отсутствие любого из них отдаёт 400, дефолта на «отсутствует — значит 0» нет: единственный клиент (megaapp-back) выкатывается синхронно с Flatline (hard cutover, см. корневой план п.2), совместимость со старым контрактом не нужна. `Poller` передаёt все три floor явным `0` (валидное значение «без доп. границы», не дефолт по умолчанию). `filterPointsForRelay` на стороне megaapp-back удалён целиком — фильтрация теперь только в Flatline.

## Как сейчас

- `internal/metrics/service.go:13-18` — `MetricPoint{Service, Name, Bucket, Value}`, без гранулярности.
- `internal/metrics/flatline_client.go` — `PushSnapshots`/`Since(ctx, cursor)`, структуры `outboundSnapshot`/`sinceResponse` без гранулярности.
- `internal/metrics/poller.go:17-43,79-114` — один курсор (`Poller.cursor`), один тик опрашивает Flatline один раз, продвигает курсор по `max(point.Bucket)` (`poller.go:89-92`), результат — raw overwrite в `latest`/`lastBucket` (health-канал) + `BroadcastDetail` всем подписчикам.
- `internal/metrics/exporter.go` — собственные метрики megaapp (food/backup-счётчики) идут в Flatline через `outbox`+`ack` (`exporter.go:90-127`), независимо от `Poller`-направления; всегда минутные по своей природе.
- `internal/metrics/ws_handlers.go:9-38` — `METRICS_SUBSCRIBE` отдаёт `flatlineClient.Since(ctx, cursor)` один раз, без окна релея — отдаёт буквально всё, что вернул Flatline с указанного курсора.

## Как должно быть

### Контракт с Flatline
- `MetricPoint.Granularity string` — везде, где сейчас `Bucket`/`Value`.
- `FlatlineClient.Since(ctx, cursor int64)` — **сигнатура не меняется**. Меняется только то, что Flatline теперь сравнивает курсор с `bucket + step(granularity)`, а не с плоским `bucket` (см. Flatline-план) — это целиком внутри Flatline, megaapp-back ничего об этом знать не нужно.
- `FlatlineClient.PushSnapshots`/`outboundSnapshot`/`pushRequest` — получают `Granularity` (для пушей megaapp всегда `"minute"`, см. ниже).
- `Exporter.FlushAndPush`/`MinuteSnapshot` — собственные метрики megaapp всегда минутные → передают `Granularity: "minute"` явно, без дефолта.

### Poller — без изменений в курсорной механике
- `Poller.cursor`, `Poller.tick`, продвижение по `max(point.Bucket)` — **не меняются вообще**. Один опрос за тик, как сейчас. Курсорная логика уже делает ровно то, что нужно — Flatline просто стал умнее в том, что отдаёт по этому курсору.
- `latest`/`lastBucket` (health-канал, `BroadcastLatest`/`METRICS_LATEST`) — не трогается по сути; продолжает обновляться по всем точкам без разбора гранулярности (как и сейчас) — для health-индикатора это не важно.
- `BroadcastDetail` — без изменений в форме (один `DetailUpdate.Points[]` на тик), точки уже несут `Granularity`, фронт различает сам. Час/день в обычном тике почти всегда отсутствуют (новая свеча — раз в час/день) — ожидаемо.
- Известный, осознанно принятый компромисс (см. корневой план, п.3): если агрегирующая джоба Flatline надолго зависнет, просроченная свеча, досчитанная с опозданием, может не попасть в текущий поток `BroadcastDetail` для уже подключённых клиентов — появится при следующем `METRICS_SUBSCRIBE` (новое подключение всегда строит выборку с нуля). Ничего не делать с этим в рамках текущей задачи.

### METRICS_SUBSCRIBE — окно релея, фильтр в Go
- `ws_handlers.go` — вместо прямой передачи клиентского `cursor` в Flatline как есть, обработчик:
  1. Запрашивает у Flatline `Since(ctx, 0)` — единственный вызов.
  2. Фильтрует результат в Go по возрасту **на гранулярность отдельно**, по `point.Bucket`: `minute` — не старше **1 суток**, `hour` — не старше **1 месяца**, `day` — не старше **1 года**. Клиентский `cursor` из сообщения **не используется** ни для одной гранулярности (см. Ревизия 3) — каждый `(re)subscribe` всегда отдаёт полное окно релея, без какого-либо клиентского состояния.
  3. Отдаёт всё через один `METRICS_UPDATE`, как сейчас.
- Числа (1 сутки/1 месяц/1 год) — константы уровня пакета, не конфиг — привязаны к архитектурному решению.

### Что не меняется
- Никакой новой таблицы/миграции в megaapp-back для этой фичи.
- `AdminLister`/admin-доступ, `Realtime.Subscribe`/`Unsubscribe` — без изменений.
- Персист курсора `Poller` на диск — рассмотрен и отклонён как часть этой фичи — раз-два минуты дублей при рестарте безвредны и сейчас, без рollup-движка в megaapp-back это никак не усугубляется.

## Чеклист

- ✅ `MetricPoint` — поле `Granularity` (+ константы `GranularityMinute`/`Hour`/`Day`).
- ✅ `FlatlineClient` (`outboundSnapshot`, `pushRequest`, `sinceResponse`) — `Granularity` на пуш и на чтение; `Since` без изменений в сигнатуре.
- ✅ `Exporter`/`MinuteSnapshot` — явный `Granularity: "minute"` при пуше (выставляется в `FlatlineClient.PushSnapshots`).
- ✅ `ws_handlers.go` — `METRICS_SUBSCRIBE`: один `Since(0)`, `filterPointsForRelay` фильтрует по возрасту в Go (per-granularity: 1 сутки/30 дней/365 дней).
- ✅ Юнит-тесты: `flatline_client_test.go` (`Granularity` в запросе), `ws_handlers_test.go` (все три окна релея), `realtime_test.go` (обновлён сигнатурой). `poller_test.go`/`exporter_test.go` — без изменений в логике, прошли как есть.
- ✅ Ревизия 3 (баг): клиентский `cursor` убран полностью (бэк и фронт), хендлер `METRICS_SUBSCRIBE` логирует ошибки `IsAdmin`/`Since`/`SendJSON` через `slog` вместо молчаливого `continue` в `readLoop`.
- ✅ `go build ./...`, `go vet ./...`, `go test ./...`, `gofmt -l .` — чисто.
