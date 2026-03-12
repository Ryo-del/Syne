## Developer function reference (core/*)

Этот документ описывает **как работает каждая функция** в `core/` (исключая `cli/`), с точки зрения разработки: входы/выходы, побочные эффекты, формат данных, и важные инварианты.

Если вы меняете поведение функций — обновляйте описание здесь вместе с кодом.

---

### `core/protocol/messages.go`

#### `func (t MessageType) String() string`

- **Назначение**: человекочитаемое имя типа сообщения для логов/UI.
- **Поведение**: маппит известные `MessageType` на строки (`join`, `chat`, …), иначе возвращает `unknown(<n>)`.
- **Побочные эффекты**: нет.

#### `func MarshalMessage(msg Message) ([]byte, error)`

- **Назначение**: сериализация `protocol.Message` в JSON для wire-формата.
- **Важно**: это *чистая* сериализация — не делает валидацию. Обычно вызывайте `ValidateMessage` до отправки.

#### `func UnmarshalMessage(data []byte) (Message, error)`

- **Назначение**: распарсить JSON в `protocol.Message`.
- **Важно**: не валидирует содержимое. После parse обычно вызывается `ValidateMessage`.

#### `func NewJoin(chatID, fromID, targetID string) Message`

- **Назначение**: конструктор `MsgJoin` для handshake “я открыл чат”.
- **Что заполняет**: `Version`, `Type`, `Target=TargetPeer`, `ChatID`, `From`, `TargetID`, `Timestamp=now`.

#### `func NewJoinAck(chatID, fromID, targetID string) Message`

- **Назначение**: конструктор `MsgJoinAck` — ответ на `NewJoin`.
- **Что заполняет**: те же поля, но `Type=MsgJoinAck`.

#### `func ValidateMessage(msg Message) error`

- **Назначение**: защита от мусора/несовместимых сообщений до обработки.
- **Проверяет**:
  - `Version == ProtocolVersion`
  - `From` не пустой
  - `Target` ∈ {`TargetPeer`, `TargetGroup`, `TargetBroadcast`}
  - `TargetID` обязателен, если `Target != TargetBroadcast`
  - `ChatID` обязателен
  - `Timestamp > 0`
  - для `MsgChat`: `ttl >= 0`
- **Побочные эффекты**: нет.

---

### `core/transport/tcp.go`

#### `func ListenTCP(port int) (*net.TCPListener, error)`

- **Назначение**: поднять TCP listener на указанном порту.
- **Возвращает**: `*net.TCPListener`, который принимает входящие соединения.
- **Побочные эффекты**: печатает “Listening on port …” в stdout.

#### `func SendTCP(peer *Peer, data []byte) error`

- **Назначение**: “dial-per-message” отправка байт на `peer.Addr`.
- **Инвариант**: `peer != nil` и `peer.Addr != nil`, иначе ошибка.
- **Побочные эффекты**: создаёт TCP соединение, пишет `data`, закрывает соединение.

#### `func AcceptTCP(listener *net.TCPListener) (net.Conn, *net.TCPAddr, error)`

- **Назначение**: обёртка над `listener.Accept()` с попыткой привести `RemoteAddr()` к `*net.TCPAddr`.
- **Возвращает**: соединение и адрес отправителя.

#### `func ReceiveTCP(conn net.Conn, maxBytes int64) ([]byte, error)`

- **Назначение**: прочитать payload целиком из `conn` с лимитом размера.
- **Поведение**:
  - читает до `maxBytes + 1` через `io.LimitedReader`
  - если данных > `maxBytes` — возвращает ошибку “payload too large”
  - всегда закрывает `conn` (через `defer conn.Close()`)
- **Важно**: “фрейминга” нет — предполагается 1 сообщение = 1 соединение (или что отправитель закрывает поток).

#### `func IsPortFree(port int) bool`

- **Назначение**: утилита для “подобрать следующий свободный порт”.
- **Метод**: пытается `net.Listen("tcp", :port)` и сразу закрывает.

---

### `core/discovery/lan.go`

#### `func StartLANDiscovery(ctx context.Context, localID string, tcpPort int, onPeer func(peerID, addr string)) error`

- **Назначение**: запустить LAN discovery:
  - слушать UDP `DiscoveryPort` на `0.0.0.0`
  - периодически слать broadcast-анонс на `255.255.255.255:DiscoveryPort`
- **Wire-формат**: строка `SYNE1|<peer_id>|<tcp_port>`.
- **Callback**: при получении валидного анонса вызывает `onPeer(peerID, "ip:port")`.
- **Инварианты**:
  - `onPeer` обязателен
  - игнорируется `peer_id == ""` и `peer_id == localID`
- **Shutdown**: по `ctx.Done()` закрывает UDP-сокеты.

---

### `core/chat/chat.go` (контакты)

#### `func (c Contact) Address() string`

- **Назначение**: собрать `ip:port` в корректном формате.
- **Важно**: использует `net.JoinHostPort`, чтобы корректно обработать IPv6.

#### `func AddContact(c Contact) error`

- **Назначение**: добавить или обновить контакт.
- **Нормализация**: `TrimSpace` всех полей.
- **Валидация**:
  - обязательны `name`, `peer_id`, `ip`, `port`
  - `net.ResolveTCPAddr("tcp", c.Address())` должен проходить
- **Обновление**: если существует контакт с тем же `peer_id` или тем же `name` (case-insensitive) — перезаписывается, иначе добавляется в конец.
- **Хранение**: пишет файл JSONL через `writeContacts`.

#### `func ListContacts() ([]Contact, error)`

- **Назначение**: прочитать список контактов из `data/contacts/contacts.jsonl`.
- **Формат**:
  - основной: JSONL (каждая строка — объект)
  - обратная совместимость: если файл начинается с `[` — пробует распарсить как JSON array
- **Отсутствие файла**: возвращает пустой список без ошибки.

#### `func FindContact(query string) (Contact, error)`

- **Назначение**: найти контакт по `peer_id` или `name` (case-insensitive).
- **Важно**: перебор по результату `ListContacts`, сложность \(O(n)\).

#### `func RenameContact(query, newName string) error`

- **Назначение**: переименовать контакт (по `peer_id` или старому `name`).
- **Инвариант**: `newName` должен быть уникальным по `name` (case-insensitive), иначе ошибка.
- **Побочные эффекты**: перезаписывает файл контактов.

#### `func DeleteContact(query string) error`

- **Назначение**: удалить контакт по `peer_id` или `name`.
- **Побочные эффекты**: перезаписывает файл контактов.

#### `func ContactFilePath() (string, error)`

- **Назначение**: вернуть путь к файлу контактов, гарантируя наличие директории.
- **Побочные эффекты**: `os.MkdirAll(data/contacts, 0755)`.

---

### `core/chat/blocklist.go` (чёрный список)

#### `func IsBlocked(peerID string) (bool, error)`

- **Назначение**: проверка “peer в ЧС?”.
- **Важно**: работает по `peer_id` и читает список через `ListBlocked`.

#### `func AddBlocked(query, reason string) error`

- **Назначение**: добавить `peer_id` в ЧС.
- **Ввод**:
  - `query` может быть `peer_id` или именем контакта
  - если `query` — контакт, берётся его `PeerID` и `Name`
- **Поведение при повторном добавлении**:
  - обновляет `Name`/`Reason` (если переданы) вместо дублирования записи
- **Хранение**: JSONL через `writeBlocked`.

#### `func RemoveBlocked(query string) error`

- **Назначение**: убрать из ЧС по `peer_id` или по сохранённому `Name` (case-insensitive).

#### `func ListBlocked() ([]BlockedPeer, error)`

- **Назначение**: прочитать ЧС из `data/blocklist/blocklist.jsonl`.
- **Формат**:
  - основной: JSONL
  - обратная совместимость: JSON array (если файл начинается с `[`).
- **Отсутствие файла**: пустой список без ошибки.

---

### `core/history/storage.go`

#### `func SaveMessage(msg protocol.Message) error`

- **Назначение**: append сообщения в историю чата.
- **Файл**: определяется `historyFilePath(msg.ChatID)` → `data/history/<sha256(chat_id)>.jsonl`.
- **Лок**: берёт lock-файл `<history>.lock` через `acquireFileLock` (O_EXCL).
- **Дедупликация**:
  - если `msg.MessageID != ""`, то сканирует файл `hasMessageIDInFile` и не пишет повторно.
- **Побочные эффекты**: создаёт директорию истории, создаёт/пишет файлы.

#### `func LoadMessages(chatID string) ([]StoredMessage, error)`

- **Назначение**: прочитать JSONL историю чата в память.
- **Отсутствие файла**: пустой список без ошибки.

---

### `core/messaging/messenger.go`

#### `func NewMessenger(peers map[string]*transport.Peer, bufSize int) *Messenger`

- **Назначение**: создать `Messenger` и построить индекс `addr -> peer_id` из стартового `peers`.
- **Inbox**: создаёт `chan protocol.Message` с буфером `bufSize`.

#### `func (m *Messenger) RegisterPeer(peerID string, peer *transport.Peer)`

- **Назначение**: зарегистрировать или обновить peer в `m.Peers` и `addrIndex`.
- **Нулевые значения**: если `peer == nil` или `peer.Addr == nil` — просто return.

#### `func (m *Messenger) ResolvePeerIDByAddr(addr *net.TCPAddr) (string, error)`

- **Назначение**: по адресу отправителя найти `peer_id` (если он ранее зарегистрирован).
- **Ошибки**:
  - `addr == nil`
  - адрес не найден в индексе

---

### `core/transport/p2p/drop.go`

#### `func IsMessageForMe(msg StoredMessage, localID string) bool`

- **Назначение**: утилита-предикат “сообщение адресовано мне?” по равенству `TargetID == localID`.
- **Важно**: это отдельный тип `drop.StoredMessage` (не `history.StoredMessage`), сейчас это эксперимент/черновик.

#### `func DropNext()`

- **Назначение**: заглушка (пока ничего не делает).

---

### `core/crypto/keys.go`

В этом пакете пока только тип:

- `type KeyPair struct { PublicKey []byte; PrivateKey []byte }`

Функциональность генерации/хранения/подписей ещё не реализована.

---

### `core/transport/relay.go`

- `type RelayTransport struct{}`

Заглушка для будущего relay-транспорта.

---

### `core/repo/repo.go`

Пакет существует как задел, но пока пустой (нет типов/функций).

