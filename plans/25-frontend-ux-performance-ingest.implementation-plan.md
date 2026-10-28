# 25. Временный WebSocket-приём фронтенд-замеров

## Реализовано

- Временный handler принимает batch, доверенно добавляет user/client/receive fields, одним append пишет NDJSON, делает `Sync` и отвечает ACK.
- Active NDJSON ротируется перед 1 GB: timestamped имя, `logs-archive`, асинхронный ZIP и удаление закрытого файла повторяют механизм Spread Capture Bot.
- При выключенном флаге batch подтверждается как discarded без операций с файлом.

## Цель

Принять временные фронтенд-замеры через уже аутентифицированный WebSocket и append-only записать их в один NDJSON-файл. После недели фронт и бэк удаляются целиком.

## Решение

```text
PERFORMANCE_METRICS_BATCH
  → текущий WebSocket Hub
  → временный handler
  → один append всего batch в timestamped active NDJSON в DATA_DIR
  → fsync + close
  → PERFORMANCE_METRICS_ACK
```

Новый HTTP route, БД, migration, очередь, background job, cache, агрегатор, WebSocket connection и доменный модуль не нужны.

## Почему WebSocket

- Соединение уже проходит JWT-проверку и несёт доверенный user id.
- Фронт отправляет только при открытом сокете и активности приложения.
- Новый transport, interceptor и отдельная авторизация не появляются.
- В существующем Hub уже есть маршрутизация typed message по `type`; нужен один дополнительный handler и ACK обратно в тот же client.

## Wire contract

### Client → server

- Новый type: `PERFORMANCE_METRICS_BATCH`.
- Payload: `batchId` и ограниченный массив готовых frontend event records.
- Максимальный кодированный batch — 48 KiB: он укладывается в действующий 64 KiB WebSocket read limit без его изменения.
- User id не принимается из payload. Его источник — authenticated WebSocket client.

### Server → client

- Новый type: `PERFORMANCE_METRICS_ACK`.
- Payload: `batchId`, список принятых `eventId` и состояние `accepted` либо `discarded`.
- `accepted` разрешён только после успешных append, `fsync` и close.
- При ошибке записи handler не отправляет ACK. Фронт оставляет batch в LocalStorage и повторит его позднее.
- При выключенном сборе handler отправляет `discarded`, не пишет файл. Это освобождает локальную очередь временно выключенной кампании.

## NDJSON

- Active: `DATA_DIR/frontend-performance-YYYY-MM-DDTHH-MM-SS.ndjson`.
- Перед первым новым append legacy `DATA_DIR/frontend-performance.ndjson` переименовывается в active-файл; строки сохраняются.
- Одна строка — одно событие, не batch. Сервер добавляет `receivedAt`, authenticated `userId` и текущий connection `clientId`; остальные поля — уже нормализованный фронтенд-замер.
- Запись batch собирается в память и выполняется одним append под mutex. Одновременные WebSocket clients не перемешают строки.
- После append выполняются `Sync` и `Close`; только затем ACK. Частота — максимум один небольшой batch на активного пользователя за 10 минут, поэтому цена durability пренебрежима.
- Перед переполнением 1 GB active-файл переименовывается в `frontend-performance-<openedAt>--<closedAt>.ndjson`, асинхронно упаковывается в `DATA_DIR/logs-archive/*.ndjson.zip`, затем закрытый NDJSON удаляется. Алгоритм и timestamp format повторяют Spread Capture Bot.
- Повтор batch после потерянного ACK может создать дубль. NDJSON сохраняет стабильный `eventId`, экспорт/анализ дедуплицирует его. Это простая at-least-once доставка без серверного состояния.

## Ограничения входа

Handler не пытается понять доменную операцию и не требует фиксированный перечень `operation`: это агностичный временный приёмник. Он делает только дешёвые синтаксические защиты WebSocket и файла:

- type, payload, batch id и event id должны декодироваться;
- batch ограничен существующим размером сообщения и числом записей;
- строки и numeric values не могут быть явно некорректными/неограниченными;
- сервер никогда не использует переданный user id, путь файла или произвольное имя файла.

Некорректный batch не должен ронять connection, писать частичные данные или затрагивать приложение. Он остаётся без ACK, а проблема видна в обычном server log.

## Включение и удаление

- Новый `PERFORMANCE_METRICS_ENABLED`, по умолчанию `false`.
- При `false` handler остаётся доступным только для discard-ACK; файловых операций нет.
- При `true` путь всегда строится от `DATA_DIR`; отдельная конфигурация пути не нужна.
- Перед неделей: включить флаг и проверить файл/права на `DATA_DIR`.
- После выгрузки: выключить флаг, сохранить нужный NDJSON вне runtime data dir, затем удалить handler, wire types, config и фронтенд-сборщик.

## Проверки при реализации

- Unit: кодирование одной NDJSON строки, batch append, mutex-конкуренция, ошибка `Sync`, disabled discard.
- WebSocket integration: authenticated client получает ACK только после записи; reconnect без ACK повторяет batch; user id в файле берётся из claims, а не payload.
- Проверить размер 48 KiB и отказ сверх лимита без partial append.
- Запустить обычные Go tests и build после изменений.

## Чеклист

- ✅ Добавить два WebSocket message type и frontend wire types.
- ✅ Добавить временный append-only writer с mutex, `Sync` и `Close`.
- ✅ Зарегистрировать один handler рядом с текущими WebSocket handlers.
- ✅ Добавить `PERFORMANCE_METRICS_ENABLED=false` в config/env examples.
- ✅ Добавить unit tests декодирования и append NDJSON.
- ✅ Перенести ротацию 1 GB и ZIP-архивацию из Spread Capture Bot, включая legacy migration.
- ✅ Запустить Go tests и build.
- ⭕ Проверить WebSocket integration: ACK после записи, reconnect без ACK, disabled discard и лимит batch.
- ⭕ Проверить путь/права production `DATA_DIR`, включить флаг на неделю.
- ⭕ Выгрузить и дедуплицировать NDJSON по `eventId`.
- ⭕ Удалить временную систему после исследования.
