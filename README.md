# Admin Bot — Telegram-бот для управления Piscine

Бот для администраторов школы программирования [Tomorrow School](https://tomorrow-school.com). Автоматически отправляет анонсы по расписанию в чат админов. Создаёт Google Sheets таблицы защит по нажатию кнопки.

## Что делает бот

Бот обслуживает шесть параллельных Piscine (все по 4 недели, включая неделю финального экзамена):

- **Piscine Go** и **Piscine JS** — с хакатоном на 3-й неделе;
- **Piscine AI 1 / AI 2 / AI 3** — три независимых параллельных потока одной AI-программы (различаются `parent.id`);
- **Piscine RUST** — без захардкоженных имён рейдов (generic-логика, номер недели по порядку рейдов).

Каждую неделю бот автоматически определяет текущую неделю через 01-edu API и отправляет нужные сообщения:

| День | Время | Сообщение | Недели |
|------|-------|-----------|--------|
| Пн | 10:00 | FAQ для новичков | только 1-я |
| Чт | 14:30 | Анонс экзамена + регистрация на рейд | 1–3 (не финальная) |
| Чт | 14:00 | Анонс Final Exam | только финальная |
| Пт | 10:00 | Анонс хакатона (Go и JS) | только 3-я |
| Вс | 15:00 | Напоминание о таблице защит + сообщение студентам | 1–3 (не финальная) |

Воскресное сообщение для админов содержит inline-кнопки для создания Google Sheets таблицы защит.

## Архитектура

```
cmd/bot/main.go                          — точка входа
internal/
├── config/                              — конфигурация из .env
├── domain/                              — типы, интерфейсы (порты)
│   ├── piscine.go                       — PiscineType, RaidInfo, маппинг рейд→неделя
│   ├── message.go                       — MessageType (6 типов сообщений)
│   ├── ports.go                         — интерфейсы OneEduClient, BotSender и др.
│   └── errors.go                        — доменные ошибки
├── usecase/
│   ├── raid.go                          — RaidUseCase (определение недели, сборка сообщений)
│   ├── defense.go                       — расчёт таблицы защит (строки, перерывы, время)
│   └── strategy/                        — стратегии по типам Piscine
│       ├── strategy.go                  — интерфейс PiscineStrategy
│       ├── base.go                      — общая логика (SupportsMessage, Type, TemplateVars)
│       ├── go.go, js.go, ai.go          — конкретные стратегии
├── delivery/telegram/
│   ├── handler.go                       — обработчики команд и callback'ов
│   └── router.go                        — регистрация обработчиков
├── infra/
│   ├── oneedu/                          — HTTP-клиент к 01-edu GraphQL API
│   │   ├── client.go                    — runQuery + GetCurrentPiscineID, GetRaidsByPiscineID, ...
│   │   ├── types.go                     — структуры GraphQL-ответов
│   │   └── queries/
│   │       ├── loader.go                — embed + извлечение query по operationName (per-file cache, thread-safe)
│   │       └── raids.graphql            — 5 GraphQL-запросов
│   ├── scheduler/scheduler.go           — CronScheduler (robfig/cron)
│   ├── telegram/adapter.go              — обёртка над go-telegram/bot
│   ├── templates/loader.go              — рендеринг .txt шаблонов (single-pass regex)
│   └── sheets/client.go                 — Google Sheets API v4
messages/                                — шаблоны сообщений (.txt)
```

## Быстрый старт

### Требования

- Go 1.25+
- Telegram Bot Token (от [@BotFather](https://t.me/BotFather))
- Доступ к 01-edu API (base URL + access token)
- Google Cloud сервисный аккаунт (для Google Sheets, опционально)

### Установка

```bash
git clone <repo-url>
cd admin-bot

# Подтянуть зависимости из go.mod
go mod tidy
```

### Конфигурация

Скопируйте `.env.example` в `.env` и заполните:

```bash
cp .env.example .env
```

| Переменная | Обязательная | Описание |
|---|---|---|
| `TELEGRAM_TOKEN` | да | Токен Telegram-бота |
| `ONEEDU_BASE_URL` | да | URL платформы 01-edu (например `01.tomorrow-school.ai`); схема `https://` подставляется автоматически |
| `PLATFORM_ACCESS_TOKEN` | да | Access token для 01-edu API |
| `APPLY_BASE_URL` | нет | URL сайта заявок (например `apply.tomorrow-school.ai`); при заданных `APPLY_BASE_URL` **и** `APPLY_ACCESS_TOKEN` в `/get_region_updates` добавляется строка «заявок с сайта (сегодня / вчера)» по каждому городу — данные из `/api/export.json`, дни считаются в часовом поясе `TIMEZONE` |
| `APPLY_ACCESS_TOKEN` | нет | Bearer-токен для API сайта заявок |
| `CHAT_IDS` | да | ID чатов-получателей рассылки через запятую (например `-100123456789,-100987654321`) |
| `SUPER_ADMIN_USER_ID` | да | Единственный user ID, который принимает запросы доступа и жмёт Approve/Reject. Всегда авторизован |
| `ADMIN_USER_IDS` | нет | Список user ID через запятую, предодобряемых при первом старте (готовый allowlist) |
| `ACCESS_STORE_PATH` | нет | Путь к JSON-файлу одобренных пользователей (по умолчанию `data/access.json`) |
| `ADMIN_CHAT_IDS` | нет | Allowlist групповых чатов, где одобренные пользователи могут выполнять команды (по умолчанию = `CHAT_IDS`) |
| `TIMEZONE` | нет | Часовой пояс для cron и расчёта дат (по умолчанию `Asia/Almaty`) |
| `TEMPLATES_PATH` | нет | Путь к шаблонам (по умолчанию `messages`) |
| `GOOGLE_CREDENTIALS_FILE` | нет | Путь к JSON-ключу сервисного аккаунта Google (для локального запуска / монтирования файла) |
| `GOOGLE_CREDENTIALS_JSON` | нет | Инлайн-содержимое JSON-ключа Google (для платформ без монтирования файлов, напр. Railway). Записывается во временный файл на старте |
| `SHEET_GO_WEEK{1..3}`, `SHEET_JS_WEEK{1..3}`, `SHEET_AI{1..3}_WEEK{1..4}` | нет | URL Google-таблиц защит по пискине и неделе |
| `SHEET_UNIVERSAL` | нет | Общая fallback-таблица защит для пискин без своей (Piscine RUST и динамически найденные пулы) |
| `REGION_<NAME>_{CHECKIN,PISCINE,MODULE}_EVENT_ID` | нет | Пины авторитетных event ID по региону для `/get_region_updates` (см. заметку ниже) |

> **Регион-события.** Источник истины для `/get_region_updates` — это event ID, а не имя/порядок события. Любая метрика без пина откатывается на дефолтный поиск по path, так что пустые значения сохраняют прежнее поведение. Формат: `REGION_ASTANAHUB_CHECKIN_EVENT_ID=12345` (имя региона — регистронезависимо).

> **`GOOGLE_FOLDER_ID`.** Присутствует в `.env.example`, но **сейчас не читается кодом** — это неиспользуемая переменная (кандидат на удаление или на доработку логики размещения таблиц в конкретной папке Drive).

### Настройка Google Sheets (опционально)

1. Создайте проект в [Google Cloud Console](https://console.cloud.google.com/)
2. Включите **Google Sheets API** и **Google Drive API**
3. Создайте **Service Account** → скачайте JSON-ключ
4. Дайте сервисному аккаунту доступ к нужным таблицам (Share → email сервисного аккаунта)
5. Подключите креды одним из двух способов:
   - **Локально / свой сервер:** положите JSON в корень (например `credentials.json`) и укажите `GOOGLE_CREDENTIALS_FILE=credentials.json`.
   - **Railway / платформы без файлов:** вставьте всё содержимое JSON в переменную `GOOGLE_CREDENTIALS_JSON` — на старте бот запишет его во временный файл и подхватит автоматически.

Без этой настройки бот работает, но кнопка «Создать таблицу» будет недоступна.

### Запуск

```bash
go run cmd/bot/main.go
```

## Команды бота

| Команда | Описание |
|---------|----------|
| `/help` | Список команд |
| `/week` | Текущая неделя для всех Piscine |
| `/raidgo` | Информация о рейде Piscine Go |
| `/raidjs` | Информация о рейде Piscine JS |
| `/raidai1` | Информация о рейде Piscine AI 1 |
| `/raidai2` | Информация о рейде Piscine AI 2 |
| `/raidai3` | Информация о рейде Piscine AI 3 |
| `/raidrust` | Информация о рейде Piscine RUST |
| `/create_tables` | Создать Google Sheets таблицы для всех активных рейдов |
| `/get_astana_updates` | Статистика обновлений Astana |
| `/get_region_updates` | Статистика по регионам (кроме Astana) |

## Как работает определение недели

1. Запрос `GetCurrentPiscineId` — получает ID активного Piscine (по `startAt <= now() <= endAt`)
2. Запрос `GetRaidsByPiscine*Id` — получает все рейды этого Piscine
3. `findActiveRaid` — ищет активный рейд (где `startAt <= now <= endAt`)
4. Если активного нет, `countEndedRaids` — если все рейды закончились, это финальная неделя
5. Иначе `findNextUpcomingRaid` — следующий рейд определяет неделю

По имени рейда определяется номер недели через маппинг в `domain.RaidWeekMap`:

| Piscine Go | Piscine JS | Piscine AI 1/2/3 |
|---|---|---|
| quad → неделя 1 | crossword → неделя 1 | backtesting-sp500 → неделя 1 |
| sudoku → неделя 2 | sortable → неделя 2 | forest-prediction → неделя 2 |
| quadchecker → неделя 3 | clonernews → неделя 3 | |
| (нет) → неделя 4 = Final | (нет) → неделя 4 = Final | |

Три потока Piscine AI используют одинаковую карту рейдов (одна программа, разные `parent.id`).
Piscine RUST не имеет захардкоженных имён рейдов: рейды берутся generic-запросом
`GetRaidsByParentId`, а номер недели вычисляется по порядку рейдов (`startAt` по возрастанию).

## Расчёт таблицы защит

Для N команд (`usecase.CalculateDefenseSchedule`):

- **Строк** = ceil(N / 3)
- **Слотов** = строк × 3
- **Перерывы**: < 5 строк — нет; 5–10 — один (в середине); > 10 — два (на трети). Дополнительные перерывы вставляются, если иначе будет > 5 строк подряд без перерыва.
- **Время**: старт 11:00, шаг 30 минут, перерыв 30 минут.

Пример для 35 команд: 12 строк × 3 = 36 слотов, перерывы в 13:00 и 15:30, расписание 11:00–17:30.

## Тестирование

```bash
# Все тесты с детектором гонок
go test -race ./...

# С подробным выводом
go test -race -v ./...

# Конкретный пакет
go test -race -v ./internal/usecase/...
go test -race -v ./internal/domain/...
go test -race -v ./internal/usecase/strategy/...
go test -race -v ./internal/infra/oneedu/queries/...
go test -race -v ./internal/infra/templates/...
go test -race -v ./internal/delivery/telegram/...
go test -race -v ./internal/config/...
```

Покрытие тестами:

| Пакет | Что тестируется | Тесты |
|---|---|---|
| `domain` | Маппинг рейд→неделя, TotalWeeks, HasHackathon, IsFinalWeek, GetRaidQueryName, инварианты RaidWeekMap | 8 |
| `usecase` | Расчёт таблицы защит, DetectCurrentWeek (все ветви), BuildMessage, BuildDefenseReminder | 24 |
| `usecase/strategy` | Полная матрица SupportsMessage (piscine × messageType × week), TemplateKey, TemplateVars | 7 |
| `config` | Загрузка env, обязательные поля, defaults, парсинг ChatIDs, схема URL | 9 |
| `infra/oneedu` | parseJWTExpiry (валидный, малформед, без exp), extractToken | 7 |
| `infra/oneedu/queries` | Загрузка операций, кэш, concurrent-safe, ошибки | 5 |
| `infra/templates` | Подстановка, missing var, repeated, empty value, no recursion, ErrTemplateNotFound | 8 |
| `delivery/telegram` | parsePiscineFromCallback, nextMonday (все дни недели + границы месяца) | 3 |

Итого: 71 тестовая функция, ~200 sub-cases.

## Управление доступом

Авторизация построена на схеме «запрос → одобрение», а не на статическом списке чатов:

1. **Супер-админ** (`SUPER_ADMIN_USER_ID`) авторизован всегда и получает запросы доступа с inline-кнопками **Approve / Reject**.
2. Новый пользователь пишет боту и запрашивает доступ; запрос уходит супер-админу, который одобряет или отклоняет его кнопкой.
3. Одобренные пользователи сохраняются в JSON-стор (`ACCESS_STORE_PATH`, по умолчанию `data/access.json`) и работают в личке с ботом без доп. настройки; в групповых чатах команды разрешены только если чат есть в `ADMIN_CHAT_IDS` (по умолчанию `= CHAT_IDS`).
4. `ADMIN_USER_IDS` — заранее одобренный allowlist: при первом старте (пустой стор) эти ID добавляются как approved, чтобы существующий список админов продолжил работать. Идемпотентно: пользователи с уже существующей записью (в т.ч. отклонённые) не перезатираются.

Стор — единственное персистентное состояние бота. При деплое в контейнере его нужно хранить на volume (см. ниже), иначе все одобренные через бота админы теряются при пересоздании контейнера (вернутся только предодобренные `ADMIN_USER_IDS`).

## Деплой

Бот работает в режиме long-polling (только исходящий трафик), поэтому **не требует `EXPOSE` и открытых портов**.

### Docker Compose (локально / свой сервер)

```bash
docker compose up -d --build      # сборка + запуск в фоне
docker compose logs -f            # смотреть логи
docker compose down               # остановить и удалить контейнер
```

Конфигурация подхватывается из `.env` (тот же файл, что читает бинарник локально). Секреты не попадают в образ: `.dockerignore` держит `.env*` вне build-контекста, compose монтирует их только на рантайме через `env_file`. Одобренные админы (`data/access.json`) переживают пересоздание контейнера благодаря именованному volume `access-data:/app/data`.

### Railway

1. Заведите переменные из `.env.example` один в один в **Variables**: обязательные `TELEGRAM_TOKEN`, `ONEEDU_BASE_URL`, `PLATFORM_ACCESS_TOKEN`, `CHAT_IDS`, `SUPER_ADMIN_USER_ID` плюс нужные опциональные (`TIMEZONE`, `ADMIN_USER_IDS`, `SHEET_*`, `REGION_*_EVENT_ID`, …).
2. Google-креды передавайте **инлайном** через `GOOGLE_CREDENTIALS_JSON` (вставьте всё содержимое JSON-ключа) — Railway не монтирует файлы, поэтому `GOOGLE_CREDENTIALS_FILE` там не подходит. На старте бот запишет JSON во временный файл и подхватит его.
3. `ACCESS_STORE_PATH` по умолчанию `data/access.json`; для персистентности одобренных админов между деплоями примонтируйте volume к `/app/data` (иначе стор сбрасывается на каждом деплое).

## Стек

- **Go** 1.25+
- [go-telegram/bot](https://github.com/go-telegram/bot) — Telegram Bot API
- [robfig/cron/v3](https://github.com/robfig/cron) — cron-планировщик
- [google.golang.org/api](https://pkg.go.dev/google.golang.org/api) — Google Sheets & Drive API
- [joho/godotenv](https://github.com/joho/godotenv) — загрузка .env
- 01-edu GraphQL API — данные о рейдах и командах

docker compose up -d --build
docker compose logs -f
docker compose down