## Syne

Syne — лёгкий **P2P-мессенджер для локальной сети** (без интернета): пиры автоматически обнаруживают друг друга по LAN и обмениваются сообщениями напрямую по TCP.

Сейчас репозиторий содержит **Go-core + CLI для ручного тестирования** и новый desktop UI в `frontend/` на **Tauri + React**, который работает через локальный Go bridge API.

## Возможности (текущее состояние)

- **LAN discovery**: UDP broadcast-анонсы присутствия.
- **Сообщения**: TCP, JSON-протокол с валидацией.
- **История**: локальная JSONL-история по `chat_id`, с дедупликацией по `message_id`.
- **Контакты**: локальный список контактов (JSONL).
- **Чёрный список (ЧС)**: блокировка пиров (JSONL), блокирует входящие `join/chat` и исходящие отправки.
- **Relay/Server**: пока заглушки/план (см. `ARCHITECTURE.md`).

## Быстрый старт

Требования:

- Go (1.20+ будет достаточно для текущего кода)

### Linux/macOS (go run)

Запуск:

```bash
go run . -port 3000 -id alice
```

Во втором терминале:

```bash
go run . -port 3001 -id bob
```

Обе копии будут вещать себя в локальной сети и “видеть” соседей.

### Windows (готовый бинарь)

Из директории, где лежит `syne.exe` (например, `Syne-main`):

```powershell
cd .\Syne-main
.\syne.exe --id Bob --port 3000
```

Во втором окне:

```powershell
.\syne.exe --id Alice --port 3001
```

## Флаги

- `-id`: ваш peer id (если пусто — генерируется UUID)
- `-port`: локальный TCP порт (если занят — возьмётся следующий свободный)
- `-peer-id`: (опционально) peer id собеседника для авто-открытия чата
- `-peer-addr`: (опционально) адрес собеседника `ip:port` для авто-открытия чата

## Команды в CLI

Откройте справку:

- `/help`

Контакты:

- `/contact add <name> <peer-id> <ip:port>`
- `/contact rename <name-or-peer-id> <new-name>`
- `/contact delete <name-or-peer-id>`
- `/contact list`

Чаты/сообщения:

- `/chat open <name-or-peer-id>` — сделать чат активным
- просто печатайте текст — он уйдёт в активный чат
- `/sendto <peer-id> <text>` — отправка без открытия чата (через форвардинг по соседям/контактам)

Чёрный список (ЧС):

- `/block add <name-or-peer-id> [reason]`
- `/block remove <name-or-peer-id>`
- `/block list`

## Где лежат данные

По умолчанию всё пишется в рабочую директорию (откуда запускаете бинарь):

- `data/contacts/contacts.jsonl` — контакты
- `data/blocklist/blocklist.jsonl` — ЧС
- `data/history/*.jsonl` — история (имя файла = sha256(chat_id))

## Документация для разработчиков

- `ARCHITECTURE.md` — компоненты и потоки данных
- `docs/DEV_FUNCTIONS.md` — описание каждой функции в `core/` (кроме `cli/`)

## Frontend

Desktop UI лежит в `frontend/`.

Что нужно для запуска:

- Go
- Node.js / npm
- Rust toolchain (для Tauri)

Go bridge API можно запустить отдельно:

```bash
go run ./cmd/syne-ui-api
```

Web UI в dev-режиме:

```bash
cd frontend
npm install
npm run dev
```

Tauri desktop:

```bash
cd frontend
npm install
npm run tauri:dev
```

## Release Build

Для standalone desktop-сборки фронтенд использует Go backend как `sidecar`.
На целевой машине пользователя не нужны `Go`, `Node.js`, `npm` или `cargo`.

Сборка релиза:

```bash
cd frontend
npm install
npm run tauri:build
```

Что происходит:

- собирается web frontend
- собирается Go binary `syne-ui-api`
- binary кладётся в `src-tauri/binaries/` с суффиксом платформы
- Tauri бандлит этот binary внутрь приложения

Важно:

- Windows `.exe` / `.msi` нормально собирать на Windows runner или Windows машине
- на macOS вы обычно собираете `.app` / `.dmg`, а не Windows `.exe`
- для cross-target сборки sidecar можно задать `SIDECAR_TARGET_TRIPLE`

Пример:

```bash
cd frontend
SIDECAR_TARGET_TRIPLE=x86_64-pc-windows-msvc npm run build:sidecar
```

Windows `.exe` удобнее собирать на Windows машине или Windows CI runner.
В репозитории есть стартовый workflow GitHub Actions: `.github/workflows/windows-build.yml`.
