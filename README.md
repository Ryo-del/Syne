## Syne

Syne — это desktop **LAN-мессенджер**: пиры находят друг друга внутри локальной сети и обмениваются сообщениями напрямую, без центрального сервера.

Текущий стек приложения:

- **Desktop UI**: `Tauri + React`
- **Локальный backend**: `Go`
- **Сетевая модель**: `P2P over LAN`

Проект теперь **UI-first**. Консольный чат удалён из репозитория, основной и единственный пользовательский entrypoint — desktop-приложение.

## Что запускать

Для обычного использования запускается desktop-приложение:

- на macOS: `Syne.app`
- на Windows: установщик или bundle из GitHub Actions

Обычному пользователю **не нужны**:

- `Go`
- `Node.js`
- `npm`
- `cargo`

Эти инструменты нужны только для разработки и сборки релизов.

## Текущее состояние

Что уже работает:

- обнаружение пиров в LAN
- доставка личных сообщений по TCP
- локальная история чатов
- контакты
- блоклист
- desktop UI

Что пока не в фокусе:

- relay/server режим
- полноценная production crypto-модель
- подписанные installers / notarized releases

## macOS

### Запуск в dev

```bash
cd frontend
npm install
npm run tauri:dev
```

### Сборка приложения

```bash
cd frontend
npm run tauri:build
```

Готовое приложение:

- `frontend/src-tauri/target/release/bundle/macos/Syne.app`

Запуск:

```bash
open frontend/src-tauri/target/release/bundle/macos/Syne.app
```

Если macOS блокирует неподписанную локальную сборку:

```bash
xattr -dr com.apple.quarantine frontend/src-tauri/target/release/bundle/macos/Syne.app
```

## Windows

В проекте уже есть Windows workflow в GitHub Actions:

- `.github/workflows/windows-build.yml`

Как забрать сборку:

1. Открой `Actions`
2. Открой успешный run `windows-build`
3. Скачай artifact `syne-windows-bundles`

Что обычно отдавать пользователю:

- `.msi`, если нужен обычный установщик
- `.exe`, если пайплайн собрал executable bundle и тебе нужен именно этот формат

## Разработка

Что нужно для разработки:

- `Go`
- `Node.js / npm`
- `Rust toolchain`

Основной desktop dev flow:

```bash
cd frontend
npm install
npm run tauri:dev
```

Что происходит:

- Vite поднимает frontend
- Go backend собирается как sidecar
- Tauri запускает desktop shell

## Release Build

Сборка desktop-релиза:

```bash
cd frontend
npm install
npm run tauri:build
```

Сейчас релизный пайплайн делает следующее:

- собирает React frontend
- собирает Go backend как sidecar binary
- бандлит sidecar внутрь desktop-приложения

Для Windows самый надёжный путь:

- собирать на Windows машине
- или использовать Windows runner в GitHub Actions

## Файлы данных

Desktop-приложение хранит runtime-данные вне исходников.

В dev-режиме, в зависимости от способа запуска backend, ты всё ещё можешь видеть локальные data-файлы, которые использует Go core.

Типичные данные:

- user identity
- contacts
- blocklist
- history

Внутри Go core всё ещё используются JSON/JSONL файлы.

## Документация для разработчиков

- `ARCHITECTURE.md`
- `docs/DEV_FUNCTIONS.md`
