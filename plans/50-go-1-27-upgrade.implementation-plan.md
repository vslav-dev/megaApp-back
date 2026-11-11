# Апгрейд до Go 1.27 и модернизация — бэкенд

Парного плана во фронте нет: фича только в `megaapp-back`. Номер 50 занят и для `megaapp-front`.

## Описание

- Поднять `go 1.26` → `go 1.27` и довести код до идиом, которые Go-команда считает актуальными.
- Закрыть уязвимости зависимостей, найденные `govulncheck` (8 достижимых).
- Порядок: безопасность и апгрейд → точечная модернизация → тесты и инструменты. Крупные развилки (JSON v2, замена `chi`) вынесены в «Решения».

## Исходное состояние (30.10.2026)

- `go.mod`: `go 1.26`. Локальный тулчейн `go1.27.3`, `GOTOOLCHAIN=auto`.
- CI (`deploy-backend.yml`): `actions/setup-go@v6` с `go-version-file: go.mod` → **прод собирается на 1.26.x, локальные проверки идут на 1.27.3**. Расхождение исчезнет после бампа.
- 136 `.go`-файлов, `golangci-lint` v2.15 с `modernize`, `intrange`, `sloglint`, `gocritic` и др. — 0 замечаний. `go vet`, `go test`, `go test -race` — зелёные.
- Прод-сервер Go не содержит: деплой — готовый бинарник (`go build -trimpath` в CI). Обновление Go на сервере не требуется.
- `chai2010/webp` — cgo (`import "C"`, встроенный libwebp). Статическая сборка с `CGO_ENABLED=0` невозможна; CI собирает нативно на `ubuntu-latest`, проблем нет.

## Последняя стабильная версия

- Go 1.27 — релиз 19.08.2026 (go.dev/doc/go1.27, go.dev/blog/go1.27). На машине стоит 1.27.3.
- Политика Go: поддерживаются две последние ветки; 1.26 уходит из поддержки с выходом 1.28 (≈02.2027).
- В блоге Go от 26.10.2026 значится срочный security-релиз (реакция на раскрытие ключа eMudhra). Содержание не проверено: перед деплоем убедиться, что CI ставит свежий патч 1.27.x.

## Официальное направление Go (что из этого важно нам)

- **`go fix` — платформа модернизаторов** (1.26, переписан на общем с `go vet` фреймворке). Рекомендация авторов: гонять `go fix ./...` при каждом апгрейде тулчейна, на чистом git, до фикс-точки (обычно два прогона). Каждый релиз приносит новые модернизаторы; в 1.27 добавлены `atomictypes`, `embedlit`, `slicesbackward`, `unsafefuncs`, `fmtappendf` удалён, `waitgroup` → `waitgroupgo`. Исправления применяются только в файлах с достаточной версией `go` в `go.mod`.
- **`//go:fix inline`** (1.26): самообслуживаемые миграции своих API. У нас нет внутренних устаревших API — не применимо.
- **Стандартная библиотека вместо ручных паттернов**: `slices`/`maps`/`cmp`, `strings.Cut*`/`SplitSeq`, `errors.AsType` (1.26), `sync.WaitGroup.Go` (1.25), `t.Context()` (1.24), `testing.B.Loop` (1.24), `new(expr)` (1.26), `atomic.Int*` и т.п. Документация `sort` с 1.22 прямо отсылает к `slices` (`slices.SortFunc` удобнее и быстрее).
- **`encoding/json`**: в 1.27 v1 работает поверх v2-движка (Unmarshal заметно быстрее, поведение сохранено, тексты ошибок могут отличаться). `encoding/json/v2` и `jsontext` — публичные пакеты. Миграция необязательна («`encoding/json` никуда не денется»). Для кода, который должен жить под обоими, рекомендован `omitzero` вместо `omitempty` для bool/чисел/указателей/структур.
- **Безопасность** (go.dev/doc/security/best-practices): `govulncheck` (в т.ч. в CI), `go vet`, `go test -race`, фаззинг, свежий патч Go. Новые инструменты стандартной библиотеки: `http.CrossOriginProtection` (1.25), `os.Root` / `os.OpenInRoot` (1.24–1.25, защита от выхода за каталог через `..` и симлинки), `ReverseProxy.Rewrite` вместо `Director` (1.26).
- **Тестирование времени**: `testing/synctest` (GA в 1.25), в 1.27 — `synctest.Sleep` и `httptest.NewTestServer` (сеть в памяти для synctest).
- **Рантайм**: Green Tea GC включён по умолчанию (1.26 — у нас уже на проде), контейнерный `GOMAXPROCS` (1.25, автоматически при `go ≥ 1.25`), size-specialized malloc (1.27, автоматически), профиль `goroutineleak` стал GA (1.27, `/debug/pprof/goroutineleak`).
- **Что 1.27 убирает**: GODEBUG `asynctimerchan`, `tls10server`, `tlsrsakex`, `tls3des`, `tlsunsafeekm`, `x509keypairleaf`, `gotypesalias`; bzr; macOS <13 (для локальной сборки; у нас macOS 15 — Darwin 24.6).
- **Опрос 2025**: роадмапа языка нет, акцент на совместимости и доверии к модулям. Крупных языковых ломающих изменений ждать не стоит.
- Источники: go.dev/doc/go1.27, go.dev/doc/go1.26, go.dev/blog/gofix, go.dev/doc/jsonv2-migration, go.dev/doc/security/best-practices, go.dev/blog/osroot, go.dev/blog/testing-time, pkg.go.dev/golang.org/x/tools/go/analysis/passes/modernize.

## Проверка бампа без изменений в репозитории (30.10.2026)

Копия `go.mod` с `go 1.27` лежала вне репозитория (`-modfile`), код не менялся.

- `go fix -diff` (с тегом `swagger` и без) — пусто: код по `modernize` уже чист, включая новые модернизаторы 1.27.
- `go vet` (оба варианта тегов) — чисто, включая новые `stdversion` и `printf` (`%w` с указателем на ошибку).
- `go test -count=1 ./...` под языковой версией 1.27 — все пакеты зелёные; `go test -tags swagger` — зелёный.
- `go test -race ./...` (текущая версия) — чисто.
- `golangci-lint run ./...` под 1.27 — 0 замечаний. `gofmt -l` — пусто.
- Вывод: бамп `go` директивы — однострочное изменение без правок кода.

Влияние остальных изменений 1.27 на нас:

- `asynctimerchan`: код использует только `time.NewTicker`/`time.After` в `select` — не зависит от буферизованных каналов таймеров.
- TLS-GODEBUG: свой TLS-сервер не поднимаем (nginx терминирует), `tls.Config` в коде нет.
- Тела HTTP/1-ответов дочитываются при Close — клиентам с default-транспортом это только улучшает переиспользование соединений (`FlatlineClient` с `DisableKeepAlives` не затронут).
- `go mod tidy` с `go 1.27` сливает дубликаты блоков `require` — у нас уже два блока (прямые/косвенные), изменений не будет.
- Директива `go ≥ 1.27` добавляет метки горутин в трейсбэки (`pprof`-метки не используем) — без эффекта.

## Находки: безопасность зависимостей (`govulncheck`, 30.10.2026)

Скан `govulncheck ./...` (и с тегом `swagger`): **8 достижимых уязвимостей в 3 модулях**, плюс 1 в импортируемых пакетах и 4 в модулях без вызова. Стандартная библиотека на 1.27.3 чистая.

| Модуль | Версия → исправлено | Суть | Где достижимо |
|---|---|---|---|
| `go-chi/chi/v5` | 5.2.3 → 5.3.0 | 3 advisory (GO-2026-5774/5775/5777): подмена IP через `X-Forwarded-For` в `middleware.RealIP` | `RealIP` в `httpx/app.go` (`chiRouter`) |
| `golang.org/x/image` | 0.42.0 → 0.45.0 | DoS: чрезмерная аллокация VP8L (GO-2026-6222); паника/лимиты в `tiff` и `webp` (GO-2026-5061/5062/5066) | `food/image_pipeline.go`: `image.Decode`, регистрация декодера `webp` |
| `aws-sdk-go-v2` (`eventstream`, `service/s3`) | eventstream 1.7.1 → 1.7.8, s3 1.88.4 → 1.97.3 | паника в декодере EventStream (GO-2026-5764) | `platform/s3/client.go` (достижимость по статическому анализу; S3 Select не используем) |

Дополнительно:

- `RealIP` не имеет потребителей: `RemoteAddr` читает только `ServerOnlyMiddleware`, которому подмена как раз вредна (комментарий в `server_only.go` держит инвариант «без прокси-заголовков `RealIP` ничего не меняет»). Проверка по наличию заголовков прокси защищает от обхода, но самый простой и надёжный вариант — **удалить `RealIP` из роутера** и вместе с ним оговорку из комментария. Версию `chi` поднять всё равно.
- Остальные зависимости отстают сильнее: `openai-go/v3` 3.40 → 3.90 (3 файла в `food/`), `aws-sdk-go-v2` 1.39 → 1.47, `s3` 1.88 → 1.114, `golang.org/x/crypto` 0.53 → 0.58, `chi` 5.2.3 → 5.5.0. Обновлять прямые зависимости по одной с полным набором проверок.
- Unmaintained по факту (новых версий нет): `disintegration/imaging` 1.6.2, `robfig/cron/v3` 3.0.1, `chai2010/webp` 1.4.0. Официальных рекомендаций по замене нет. См. «Решения».

## Находки: модернизация кода (то, что не поймал `go fix`)

Состояние уже хорошее: `errors.AsType` (4 места), `wg.Go` (3 теста), `atomic.Int32/Int64/Bool`, `t.Context()` (313), `t.TempDir`/`t.Setenv`, `min`/`max`, `slices`/`maps`, `for range n`, `slog` (83 вызова, JSON-обработчик), таймауты `http.Server`, `MaxBytesReader`, cookie `HttpOnly`+`SameSite`.

| Область | Находка | Действие |
|---|---|---|
| Ошибки | `errors.As` в `httpx/legacy/legacy.go` (`ErrorMessageOf`) — единственный | `errors.AsType`, как в соседней `ErrorKindOf` |
| Сортировка | 17 вызовов `sort.Slice`/`sort.Strings` (10 в проде: `metrics/poller.go`, `quotes/service.go`, `config/ingest.go`, `platform/sqlite/migrations.go`, `platform/rotatingfile/rotatingfile.go`, `money/service.go`, `food/service.go` ×2, `food/search_index.go`, `food/personal_kcal_job.go`; 7 в тестах) | `slices.Sort` / `slices.SortFunc` + `cmp.Compare`. `go fix` не трогает: сложные компараторы. Проверить, что порядок при равных ключах не важен (`sort.Slice` нестабилен, как и `SortFunc`) |
| Логирование | `log.Printf` в `platform/sqlite/migrations.go` — единственное использование пакета `log`, мимо JSON-логгера | перейти на `slog` (логгер передать или `slog.Default()`) |
| Рандом | `math/rand` (v1) с `rand.New(rand.NewSource(...))` в `food/personal_kcal_job.go` | `math/rand/v2` с `rand.NewPCG`. Не баг, а стиль; последовательность значений изменится — проверить тесты ГА. Низкий приоритет |
| JSON-теги | 16 тегов `omitempty` | заменить на `omitzero` у полей bool/числовых/указателей/структур (≈10), у string/slice/map оставить. Под v1 поведение идентично, под v2 `omitempty` у bool/чисел вёл бы себя иначе. Подготовка к v2 бесплатно |
| Строки | `strings.Split` + `range` в `food/http.go` (`parseCatalogueIDs`) | `strings.SplitSeq` |
| Горутины | `wg.Add(1)` + `go func` в `httpx/legacy_fixture_test.go`; `rotatingfile` держит `sync.WaitGroup` | `wg.Go` там, где паттерн совпадает |
| Сигналы | `main.go`: `signal.NotifyContext` | с 1.26 контекст отменяется с причиной (какой сигнал) — залогировать `context.Cause` при остановке |
| CSRF/Origin | собственные `auth.OriginMiddleware` + `isSameOrigin` (`auth/http.go`) | `http.CrossOriginProtection` (1.25): проверка `Sec-Fetch-Site` + `Origin` против `Host`, безопасные методы пропускает. Dev-источники (`localhost:4200/4201` при хосте `localhost:3000`) заводить через `AddTrustedOrigin` **только вне прода** — иначе ослабим прод. Поведение сверить по доке перед правкой. WebSocket (`CheckOrigin` у gorilla) остаётся отдельным |
| Файлы | пути от внешнего имени: `food/lab_debug_service.go` (`ImportCatalogue`: `filepath.Base` + суффикс), `food/image_store.go` (`VariantPath`: regexp + `Join`), `media_http.go` (`http.ServeFile`), `backup` | `os.Root` / `os.OpenInRoot` на каталоги бэкапов и картинок, раздача через `http.ServeFileFS` + `root.FS()`. Текущие проверки корректны — это упрочнение (защита от симлинков и регрессий в валидации), не исправление уязвимости |
| Тесты | 13 `time.Sleep` (`metrics/realtime_test.go` ×6, `metrics/poller_test.go`, `httpx/app_test.go`, `food/archive_test.go`, `food/search_index_test.go` ×2, `backup/http_test.go`, `food/image_regeneration_test.go`) | оценивать по одному: чистая логика таймеров → `testing/synctest`; ожидание готовности → явная синхронизация. Суммарно ≈0,5 с, выигрыш — детерминизм, не скорость. `synctest` пока 0 использований |
| Наблюдаемость | нет `net/http/pprof`; есть группа `ServerOnlyMiddleware` для debug-маршрутов | опционально: `/debug/pprof/*` (с `goroutineleak`) в той же группе — три строки |

Не применимо или не нужно сейчас:

- Generic-методы, ключи struct-литералов по селекторам, обобщённый вывод типов (1.27): нечего менять, кандидатов нет. `new(expr)` (1.26): хелперов-указателей нет (`go fix newexpr` — 0), ручной скан `v := x; &v` — по ходу правок.
- `testing.B.Loop`: бенчмарков нет. `iter.Seq`: кандидатов нет. `t.Parallel`: в тестах 62 вызова `t.Setenv`, `tparallel` включён; набор тестов идёт секунды — не стоит.
- `slog.NewMultiHandler`, `uuid`, `crypto/mldsa`, `simd`, `runtime/secret`, `ReverseProxy.Director`, `crypto/tls.Config.Rand`: не используем.
- Flight recorder и `GOMEMLIMIT`: для одного сервера без нагрузочных проблем — избыточно (см. отказ от оверинжиниринга).

## Решения, ожидающие пользователя

- **JSON v2.** Рекомендация: **не мигрировать сейчас.** v1 уже работает на v2-движке (прирост на Unmarshal бесплатно). Миграция меняет поведение: `nil`-срезы/мапы сериализуются как `[]`/`{}` вместо `null` (41 объявление `var x []T` в проде — меняется контракт с фронтом), поля матчатся с учётом регистра, дубли имён и невалидный UTF-8 — ошибки, `Duration` — ошибка, `DisallowUnknownFields` → `RejectUnknownMembers` (`settings/service.go`). 25 прод-файлов с `encoding/json`, общие помощники только в `httpx/legacy`. API v2 стабилизировался в самом 1.27 (переименованы теги, убраны опции). План: сделать `omitzero` сейчас, вернуться к v2 отдельным планом после 1–2 патчей 1.27 и с проверкой контракта фронта.
- **`chi` → `net/http.ServeMux`.** Стандартный мультиплексор умеет методы и wildcard (с 1.22). Заменить можно: в проде 11 `chi.URLParam`, 8 `.Route`, 1 `.Group`, 4 `chimiddleware`, 28 `chi.NewRouter` в тестах. Go-команда `chi` не объявляла устаревшим. Выигрыш: минус зависимость (и класс CVE как выше) ценой переписывания всех `RegisterRoutes`. Рекомендация: **отдельным планом после апгрейда**, не смешивать.
- **`imaging` / `webp` / `cron` / `gorilla/websocket`.** Рекомендация: **оставить.** Новых версий нет, но и уязвимостей `govulncheck` не нашёл (кроме `x/image`, обновляется отдельно). Замена `imaging` на `x/image/draw` потребует переписывания обработки изображений (Fill/Blur/Crop). `webp`-кодировщика вне cgo-библиотеки нет.
- **CI и `AGENTS.md`.** Предложение: добавить в локальные проверки корневого `AGENTS.md` `go test -race ./...` и `govulncheck ./...` (`go install golang.org/x/vuln/cmd/govulncheck@latest` один раз, как `golangci-lint`). В `deploy-backend.yml` не добавлять (правило: проверки только локально). Нужно подтверждение.

## Фазы и чек-лист

Каждая фаза — отдельный коммит; после каждой полный набор проверок из `AGENTS.md`.

Фаза 1. Безопасность зависимостей

- ✅ Удалить `chimiddleware.RealIP` из `chiRouter`, поправить комментарий в `server_only.go`
- ✅ Поднять `chi` до последней 5.x, `golang.org/x/image` до последней, `aws-sdk-go-v2` + `service/s3` + `eventstream` до последних
- ✅ Прогнать `govulncheck ./...` (оба варианта тегов): 0 достижимых
- ✅ Полный набор проверок

Фаза 2. Go 1.27

- ✅ `go.mod`: `go 1.27`; `go mod tidy`
- ✅ `go fix ./...` (оба варианта тегов) — ожидается пусто; `gofmt -l` — пусто
- ✅ Убедиться, что CI ставит 1.27.x (`go-version-file: go.mod`)
- ✅ Полный набор проверок + `go test -race ./...`

Фаза 3. Остальные зависимости

- ✅ `golang.org/x/crypto`, `x/sys` и транзитивные — до последних
- ✅ `openai-go/v3` 3.40 → последняя (3 файла в `food/`), с прогоном тестов провайдеров
- ✅ Полный набор проверок

Фаза 4. Модернизация кода

- ✅ `errors.AsType` в `ErrorMessageOf`
- ✅ `sort.*` → `slices.*` (10 прод + 7 тестов)
- ✅ `log.Printf` → `slog` в миграциях
- 🟡 `omitempty` → `omitzero` у bool/числовых/указательных/структурных полей (не делаем: польза только при миграции на JSON v2)
- 🟡 `strings.SplitSeq` в `parseCatalogueIDs` (не делаем: код использует `len(parts)`)
- ✅ `wg.Go` где применимо
- ✅ Причина остановки сигнала в логе `main.go`
- 🟡 `math/rand/v2` в `personal_kcal_job.go` (не делаем: меняет результаты ГА без выгоды)
- ✅ Полный набор проверок

Фаза 5. Упрочнение

- ✅ `http.CrossOriginProtection` вместо `OriginMiddleware`/`isSameOrigin`; тесты на межсайтовый запрос (dev-источники не понадобились, см. итоги фазы 5)
- 🟡 `os.Root` для каталогов бэкапов и картинок; `ServeFileFS` (не делаем: текущие проверки корректны, пользы нет)
- ✅ Полный набор проверок

Фаза 6. Тесты и инструменты

- 🟡 Аудит 13 `time.Sleep`: перевод на `synctest` или явную синхронизацию (не делаем: тесты стабильны, выигрыша нет; трогать только тест, который начнёт мигать)
- ✅ Добавить `go test -race ./...` и `govulncheck ./...` в проверки корневого `AGENTS.md`
- 🟡 Опционально: pprof (`goroutineleak`) в группе `ServerOnlyMiddleware` (не делаем: нет подозрений на утечку горутин)

## Итоги фаз 1–2 (30.10.2026)

- Фаза 1: `chi` 5.2.3 → 5.5.0, `x/image` 0.42.0 → 0.47.0, `aws-sdk-go-v2` 1.39.5 → 1.47.5, `s3` 1.88.4 → 1.114.6 (`eventstream` 1.7.1 → 1.7.25), `smithy-go` 1.23.1 → 1.28.7. `RealIP` удалён из роутера, в тесте `ServerOnlyMiddleware` обёртка `RealIP` убрана (подделка заголовков по-прежнему даёт 403). `govulncheck`: 0 достижимых уязвимостей (с тегом `swagger` и без).
- Фаза 2: `go 1.27` (без патч-версии: `go get go@1.27` записал `1.27.3`, поправлено вручную, чтобы `setup-go` брал свежий патч 1.27.x). `go fix -diff` пусто, `gofmt -l` пусто.
- Отклонение от плана: линтер `modernize` под `go 1.27` нашёл `strings.LastIndex` → `strings.CutLast` в `metrics/process_sampler.go` (`go fix` его не показал). Исправлено.
- Остаётся в `govulncheck` (код не вызывает): 4 advisory в `golang.org/x/crypto` 0.53.0 (исправлено в 0.55–0.56; одно без фикса). Закрывается в фазе 3.
- Проверки: `go build`, `go vet`, `go test -count=1`, `go test -race` — зелёные; `golangci-lint` 0 замечаний (оба варианта тегов). Не закоммичено.

## Итоги фазы 3 (30.10.2026)

- Обновлено: `x/crypto` 0.53.0 → 0.58.0, `modernc.org/sqlite` 1.58.0 → 1.61.0 (`libc` → 1.79.1), `openai-go/v3` 3.40.0 → 3.90.0, косвенные `go-humanize` 1.1.0, `go-strftime` 1.1.0, `tidwall/gjson` 1.20.0, `match` 1.2.0, `pretty` 1.2.2. `sqlite` в плане не значился, обновлён заодно как драйвер БД.
- Проверки: `go build`, `go vet`, `go test -count=1`, `go test -race` — зелёные; `golangci-lint` 0 замечаний (оба варианта тегов); `gofmt -l` пусто.
- `govulncheck`: 0 достижимых. Остался один advisory в `x/crypto` (GO-2026-5932), исправления пока нет, код его не вызывает.
- Не покрыто тестами: `openai-go` вызывается в трёх файлах `food/`, но тест через SDK есть только у OpenRouter-клиента (`ai_openrouter_test.go`). Эмбеддинги и медиа-клиент с новой версией SDK против реального API не проверялись.
- Не обновлялись: `x/net`, `x/term`, `x/sync`, `x/mod`, `x/tools`, `azure-sdk`, `golang-jwt`, `pprof` — не входят в `go.mod` или появились в графе только транзитивно; `go mod tidy` их не добавляет.

## Итоги фазы 4 (30.10.2026)

Решение по объёму (оценка «сложность против пользы»): сделаны только пункты с реальной пользой. Не делаются: `omitempty` → `omitzero` (польза только при миграции на JSON v2, которая отложена; вернуться вместе с ней), `math/rand/v2` (меняет результаты генетического алгоритма без выгоды), `strings.SplitSeq` (`parseCatalogueIDs` использует `len(parts)` для предвыделения). Пункты выше остаются ⭕ как отложенные, не как забытые.

Обозначения: ✅ сделано, ⭕ не сделано, 🟡 решено не делать (причина рядом). Позже все такие пункты переведены с ⭕ на 🟡.

Сделано:

- `errors.AsType` в `ErrorMessageOf`.
- Миграции пишут через переданный `slog`-логгер (события `migrations_skipped`, `migration_applied`, `migrations_checked`); у `ApplyMigrations` появился параметр логгера. Пакет `log` в коде больше не используется.
- `main.go` логирует `app_stopping` с причиной остановки (`context.Cause` сигнального контекста).
- Все 17 вызовов `sort.*` заменены на `slices.Sort` / `slices.SortFunc` с `cmp`; пакет `sort` в коде больше не используется.
- `wg.Go` в `legacy_fixture_test.go` и `search_index_test.go` (в `rotatingfile` уже был).

Анализ порядка при равных ключах (`sort.Slice` и `slices.SortFunc` нестабильны, порядок равных элементов может отличаться):

- Безопасно, ключи уникальны или равные значения неразличимы: `metrics/poller.go` (имена сервисов — ключи мапы), `quotes/service.go` (даты — ключи мапы), `config/ingest.go` (имена переменных окружения), `rotatingfile` (равные строки неразличимы), `food/service.go` `sortedKeys` (ключи мапы), все тестовые `sort.Strings`.
- Безопасно, порядок полный: `money/service.go` (дата, затем `ID` сделки — уникальный), `food/service.go` `rankProducts` (ключ, затем `CatalogueID`).
- Генетический алгоритм (`personal_kcal_job.go`): сортировка только по фитнесу. При равном фитнесе родителями могут стать разные геномы, но они по определению равноценны, а генератор случайных чисел и так засеян временем и `userID`, то есть прогоны не воспроизводимы. Опасности нет. Бонус: `cmp.Compare` корректно обрабатывает NaN, в отличие от `<`.
- Реальный риск был в `food/search_index.go` (`rank`): тай-брейк по имени, но имя в `foodCatalogue` не уникально (нет ограничения в миграциях, выборка `ORDER BY name`), а результат режется до 30. При равных оценке и имени порядок определялся алгоритмом сортировки и порядком строк из БД, и на границе 30-го места мог выпасть разный товар. Добавлен третий ключ — `id` по возрастанию: порядок теперь полностью определён и не зависит от реализации сортировки.

Проверки: `go build`, `go vet`, `go test -count=1`, `go test -race` — зелёные (оба варианта тегов), `golangci-lint` 0 замечаний, `go fix -diff` пусто, `gofmt -l` пусто. Не закоммичено.

## Итоги фаз 5–6 (30.10.2026)

Фаза 5. `auth.OriginMiddleware` теперь на `http.CrossOriginProtection`; свои `isSameOrigin` и `isUnsafeMethod` удалены. Ответ на отказ прежний: 403 и `{"detail":"Cross-origin request rejected"}`, контракт с фронтом не меняется. Подключение не изменилось: `OriginMiddleware` остаётся внутри `auth.Middleware` и на `register`/`login`.

Проверка по средам (код фронта и конфиги nginx читались, серверы не трогались):

- Локально: `ng serve` проксирует `/api` на `localhost:3000` с `changeOrigin` (`proxy.conf.json`), бэкенд видит `Host: localhost:3000`, а браузер считает запрос same-origin к `:4200` и шлёт `Sec-Fetch-Site: same-origin` (для `localhost` заголовок отправляется). Стандартная проверка идёт по этому заголовку и пускает запрос. Поэтому доверенные dev-источники (`AddTrustedOrigin`) не понадобились, отклонение от первоначального плана: старое исключение «Origin `localhost:4200/4201` при хосте `localhost:3000`» для браузеров было мёртвым кодом, а у бэкенда нет CORS, так что прямые запросы `4200 → 3000` невозможны в любом случае.
- Тест (`test.vslav.dev`, `megatest`) и прод (`app.vslav.dev`, `megaback`): nginx с `proxy_set_header Host $host`, фронт и API на одном origin, `Sec-Fetch-Site: same-origin`, а без него (старый браузер) `Origin` совпадает с `Host`. Swagger UI на том же origin — «Try it out» проходит. `sendBeacon` с фронта — same-origin POST, проходит. Расширение с ключом `X-Ingest-Key` проверку не проходит и раньше (ключ проверяется до сессии).
- Тесты (`httptest`): уже передавали `Origin: http://<host сервера>` без `Sec-Fetch-Site`, идут по запасному пути Origin против Host.

Таблица `internal/auth/origin_test.go` (27 случаев: среды, методы, same-origin, cross-site, same-site, `Origin: null`, запасной путь без `Sec-Fetch-Site`, безопасные методы, тело отказа) прогнана сначала на старой реализации: расхождений ровно два, оба безопасные:

- Dev через туннель (`npm run start-ngrok`, `Origin` — домен ngrok): раньше 403, теперь 200 (улучшение).
- Cross-site POST вообще без `Origin`: раньше 200, теперь 403. Браузеры всегда шлют `Origin` на кросс-сайтовом POST, на практике без изменений.

Мелкие побочные отличия: запасной путь сравнивает хост с учётом регистра (браузеры и nginx приводят его к нижнему); вручную собранный запрос с `Origin: http://localhost:4200` и без `Sec-Fetch-Site` к `localhost:3000` теперь получит 403 (curl с подделанным `Origin`; без `Origin` по-прежнему проходит). WebSocket (`isAllowedOrigin` в `ws/http.go`) не менялся: его проверка строже и относится к GET-апгрейду, стандартная защита безопасные методы не проверяет.

Фаза 6. Из шести пунктов фазы нужен был один: правка корневого `AGENTS.md` — в проверки бэкенда добавлены `go test -race ./...` и `govulncheck` (через `go run …@latest`, установка не нужна), а для апгрейда Go — `go fix -diff` и `gofmt -l`. В деплой это не добавлялось. Остальные пункты фазы (`synctest`, pprof) помечены 🟡 и не делались.

Проверки: `go build`, `go vet`, `go test -count=1`, `go test -race` — зелёные (оба варианта тегов), `golangci-lint` 0 замечаний. Не закоммичено.
