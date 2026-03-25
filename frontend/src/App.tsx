import {
  startTransition,
  useDeferredValue,
  useEffect,
  useMemo,
  useState,
} from "react";
import {
  blockPeer,
  deleteContact,
  getApiBase,
  listenEvents,
  loadBootstrap,
  loadInviteCode,
  loadMessages,
  markChatRead,
  openPrivateChat,
  renameContact,
  resolveInviteCode,
  saveContact,
  sendMessage,
  unblockPeer,
} from "./lib/api";
import type {
  AppEvent,
  ChatSummary,
  Contact,
  InviteCode,
  Snapshot,
  UIMessage,
} from "./types";

const EMPTY_CONTACT: Contact = {
  name: "",
  peer_id: "",
  ip: "192.168.",
  port: "",
};

const EMPTY_SNAPSHOT: Snapshot = {
  local_id: "",
  port: 0,
  contacts: [],
  blocked: [],
  neighbors: [],
  chats: [],
};

const EMOJI_OPTIONS = ["🙂", "😎", "🤝", "🛰️", "🌿", "🔥", "🦊", "🐼", "😇", "🌙"];

function formatTime(value: number) {
  if (!value) {
    return "";
  }
  return new Intl.DateTimeFormat([], {
    hour: "2-digit",
    minute: "2-digit",
  }).format(value);
}

function splitAddress(addr?: string) {
  if (!addr) {
    return { ip: "", port: "" };
  }
  if (addr.startsWith("/")) {
    const parts = addr.split("/");
    if (parts.length >= 5 && (parts[1] === "ip4" || parts[1] === "ip6")) {
      return { ip: parts[2], port: parts[4] };
    }
  }
  const index = addr.lastIndexOf(":");
  if (index === -1) {
    return { ip: addr.replace(/^\[|\]$/g, ""), port: "" };
  }
  return {
    ip: addr.slice(0, index).replace(/^\[|\]$/g, ""),
    port: addr.slice(index + 1),
  };
}

function buildEmptyContact(overrides?: Partial<Contact>): Contact {
  return {
    ...EMPTY_CONTACT,
    ...overrides,
  };
}

function buildContactDraft(chat: ChatSummary | null, fallbackAddr?: string): Contact {
  if (!chat) {
    return buildEmptyContact();
  }
  const { ip, port } = splitAddress(chat.known_addr || fallbackAddr);
  return buildEmptyContact({
    name: chat.title !== chat.peer_id ? chat.title : "",
    peer_id: chat.peer_id,
    ip: ip || EMPTY_CONTACT.ip,
    port,
  });
}

function upsertChat(chats: ChatSummary[], incoming: ChatSummary) {
  const next = chats.filter((item) => item.chat_id !== incoming.chat_id);
  next.unshift(incoming);
  next.sort((a, b) => {
    if (a.last_timestamp === b.last_timestamp) {
      return a.title.localeCompare(b.title);
    }
    return b.last_timestamp - a.last_timestamp;
  });
  return next;
}

function upsertNeighbor(snapshot: Snapshot, incoming: Snapshot["neighbors"][number]) {
  const neighbors = snapshot.neighbors
    .filter((item) => item.peer_id !== incoming.peer_id)
    .concat(incoming)
    .sort((a, b) => b.last_seen - a.last_seen);
  const hasContactAlias = snapshot.contacts.some(
    (item) => item.peer_id === incoming.peer_id && item.name,
  );
  const chats = snapshot.chats.map((chat) => {
    if (chat.peer_id !== incoming.peer_id) {
      return chat;
    }
    return {
      ...chat,
      known_addr: incoming.addr,
      online: true,
      title: hasContactAlias ? chat.title : incoming.name || chat.title,
    };
  });
  return {
    ...snapshot,
    neighbors,
    chats,
  };
}

function describeError(err: unknown, fallback: string) {
  if (err instanceof Error) {
    const message = err.message.trim();
    if (!message || message === "The string did not match the expected pattern.") {
      return fallback;
    }
    return message;
  }
  return fallback;
}

function readStorage<T>(key: string, fallback: T): T {
  if (typeof window === "undefined") {
    return fallback;
  }
  try {
    const raw = window.localStorage.getItem(key);
    if (!raw) {
      return fallback;
    }
    return JSON.parse(raw) as T;
  } catch {
    return fallback;
  }
}

function writeStorage<T>(key: string, value: T) {
  if (typeof window === "undefined") {
    return;
  }
  window.localStorage.setItem(key, JSON.stringify(value));
}

export default function App() {
  const [snapshot, setSnapshot] = useState<Snapshot>(EMPTY_SNAPSHOT);
  const [selectedChatId, setSelectedChatId] = useState("");
  const [messages, setMessages] = useState<Record<string, UIMessage[]>>({});
  const [query, setQuery] = useState("");
  const [composer, setComposer] = useState("");
  const [error, setError] = useState("");
  const [errorToastKey, setErrorToastKey] = useState(0);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [showNewContactPopover, setShowNewContactPopover] = useState(false);
  const [contactForm, setContactForm] = useState<Contact>(EMPTY_CONTACT);
  const [inviteCode, setInviteCode] = useState<InviteCode | null>(null);
  const [invitePeerIdDraft, setInvitePeerIdDraft] = useState("");
  const [blockReason, setBlockReason] = useState("");
  const [editingPeerName, setEditingPeerName] = useState(false);
  const [peerNameDraft, setPeerNameDraft] = useState("");
  const [emojiPickerTarget, setEmojiPickerTarget] = useState<"self" | "peer" | null>(null);
  const [selfEmoji, setSelfEmoji] = useState(() => readStorage("syne.self_emoji", "🙂"));
  const [peerEmojis, setPeerEmojis] = useState<Record<string, string>>(() =>
    readStorage("syne.peer_emojis", {}),
  );
  const deferredQuery = useDeferredValue(query);

  const selectedChat = snapshot.chats.find((item) => item.chat_id === selectedChatId) ?? null;
  const selectedContact = selectedChat
    ? snapshot.contacts.find((item) => item.peer_id === selectedChat.peer_id) ?? null
    : null;
  const selectedPeer = selectedChat
    ? snapshot.neighbors.find((item) => item.peer_id === selectedChat.peer_id) ?? null
    : null;
  const selectedMessages = selectedChat ? messages[selectedChat.chat_id] ?? [] : [];
  const selectedAddr = selectedChat?.known_addr || selectedPeer?.addr || "";
  const selectedPeerEmoji = selectedChat ? peerEmojis[selectedChat.peer_id] ?? "🙂" : "🙂";

  const filteredChats = useMemo(() => {
    const needle = deferredQuery.trim().toLowerCase();
    if (!needle) {
      return snapshot.chats;
    }
    return snapshot.chats.filter((item) => (
      item.title.toLowerCase().includes(needle) ||
      item.peer_id.toLowerCase().includes(needle)
    ));
  }, [deferredQuery, snapshot.chats]);

  async function refreshBootstrap(preserveSelection = true) {
    const nextSnapshot = await loadBootstrap();
    startTransition(() => {
      setSnapshot(nextSnapshot);
      if (!preserveSelection) {
        setSelectedChatId(nextSnapshot.chats[0]?.chat_id ?? "");
        return;
      }
      if (selectedChatId) {
        const exists = nextSnapshot.chats.some((item) => item.chat_id === selectedChatId);
        if (!exists) {
          setSelectedChatId(nextSnapshot.chats[0]?.chat_id ?? "");
        }
      } else {
        setSelectedChatId(nextSnapshot.chats[0]?.chat_id ?? "");
      }
    });
  }

  async function refreshMessages(chatId: string) {
    const items = await loadMessages(chatId);
    startTransition(() => {
      setMessages((current) => ({ ...current, [chatId]: items }));
    });
  }

  useEffect(() => {
    let mounted = true;

    async function bootstrap() {
      try {
        setLoading(true);
        await refreshBootstrap(false);
      } catch (err) {
        if (mounted) {
          setError(describeError(err, "Failed to reach local backend"));
        }
      } finally {
        if (mounted) {
          setLoading(false);
        }
      }
    }

    void bootstrap();
    return () => {
      mounted = false;
    };
  }, []);

  useEffect(() => {
    if (!selectedChatId) {
      return;
    }
    void refreshMessages(selectedChatId);
    void markChatRead(selectedChatId).catch(() => undefined);
  }, [selectedChatId]);

  useEffect(() => {
    let stop: () => void = () => { };
    try {
      stop = listenEvents((event: AppEvent) => {
        if (event.error) {
          setError(event.error);
        }
        if (event.message) {
          startTransition(() => {
            setMessages((current) => {
              const existing = current[event.message!.chat_id] ?? [];
              const nextItems = existing.some(
                (item) => item.message_id && item.message_id === event.message!.message_id,
              )
                ? existing
                : [...existing, event.message!];
              return {
                ...current,
                [event.message!.chat_id]: nextItems.sort((a, b) => a.timestamp - b.timestamp),
              };
            });
          });
        }
        if (event.chat) {
          startTransition(() => {
            setSnapshot((current) => ({
              ...current,
              chats: upsertChat(current.chats, event.chat!),
            }));
          });
        }
        if (event.type === "peer_discovered" && event.peer) {
          startTransition(() => {
            setSnapshot((current) => upsertNeighbor(current, event.peer!));
          });
        }
        if (
          event.type === "contact_added" ||
          event.type === "contact_updated" ||
          event.type === "contact_deleted" ||
          event.type === "peer_blocked" ||
          event.type === "peer_unblocked"
        ) {
          void refreshBootstrap();
        }
      });
    } catch (err) {
      setError(describeError(err, "Live updates are unavailable"));
    }
    return () => {
      stop();
    };
  }, []);

  useEffect(() => {
    if (!error) {
      return;
    }
    setErrorToastKey((current) => current + 1);
    const timer = window.setTimeout(() => {
      setError("");
    }, 4200);
    return () => {
      window.clearTimeout(timer);
    };
  }, [error]);

  useEffect(() => {
    if (!selectedChat) {
      setPeerNameDraft("");
      setEditingPeerName(false);
      setBlockReason("");
      return;
    }
    setPeerNameDraft(selectedContact?.name ?? selectedChat.title);
    setEditingPeerName(false);
    setBlockReason("");
    setEmojiPickerTarget(null);
  }, [selectedChat, selectedContact]);

  async function handleOpenPeer(peerId: string, peerAddr?: string, name?: string) {
    try {
      setError("");
      const resolvedName = (name && name !== peerId) ? name : `Anonymous ${peerId.slice(-4)}`;
      const chat = await openPrivateChat({ peer_id: peerId, peer_addr: peerAddr, name: resolvedName });
      startTransition(() => {
        setSnapshot((current) => ({
          ...current,
          chats: upsertChat(current.chats, chat),
        }));
        setSelectedChatId(chat.chat_id);
      });
      await refreshMessages(chat.chat_id);
    } catch (err) {
      setError(describeError(err, "Failed to open chat"));
    }
  }

  async function handleSend() {
    if (!selectedChat || !composer.trim()) {
      return;
    }
    const text = composer.trim();
    setComposer("");
    try {
      setError("");
      await sendMessage({
        chat_id: selectedChat.chat_id,
        target_id: selectedChat.peer_id,
        text,
      });
    } catch (err) {
      setComposer(text);
      setError(describeError(err, "Failed to send message"));
    }
  }

  async function handleSaveContact() {
    if (!contactForm.name || !contactForm.peer_id || !contactForm.ip || !contactForm.port) {
      setError("Fill all contact fields");
      return;
    }
    try {
      setSaving(true);
      setError("");
      await saveContact(contactForm);
      await refreshBootstrap();
      setShowNewContactPopover(false);
      setContactForm(buildEmptyContact());
    } catch (err) {
      setError(describeError(err, "Failed to save contact"));
    } finally {
      setSaving(false);
    }
  }

  async function handleCommitPeerName() {
    if (!selectedChat || !peerNameDraft.trim()) {
      setEditingPeerName(false);
      return;
    }
    try {
      setSaving(true);
      setError("");
      if (selectedContact) {
        await renameContact(selectedChat.peer_id, peerNameDraft.trim());
      } else {
        const draft = buildContactDraft(selectedChat, selectedAddr);
        if (!draft.ip || !draft.port) {
          setError("Peer address is not known yet. Open the peer or add the contact manually.");
          return;
        }
        await saveContact({
          ...draft,
          name: peerNameDraft.trim(),
        });
      }
      await refreshBootstrap();
      setEditingPeerName(false);
    } catch (err) {
      setError(describeError(err, "Failed to save peer name"));
    } finally {
      setSaving(false);
    }
  }

  async function handleDeleteContact() {
    if (!selectedChat) {
      return;
    }
    try {
      setSaving(true);
      setError("");
      await deleteContact(selectedChat.peer_id);
      await refreshBootstrap();
    } catch (err) {
      setError(describeError(err, "Failed to delete contact"));
    } finally {
      setSaving(false);
    }
  }

  async function handleBlock() {
    if (!selectedChat) {
      return;
    }
    try {
      setSaving(true);
      setError("");
      await blockPeer({
        query: selectedChat.peer_id,
        reason: blockReason.trim(),
      });
      setBlockReason("");
      await refreshBootstrap();
    } catch (err) {
      setError(describeError(err, "Failed to block peer"));
    } finally {
      setSaving(false);
    }
  }

  async function handleUnblock(peerId: string) {
    try {
      setSaving(true);
      setError("");
      await unblockPeer(peerId);
      await refreshBootstrap();
    } catch (err) {
      setError(describeError(err, "Failed to unblock peer"));
    } finally {
      setSaving(false);
    }
  }

  async function handleOpenInvitePeer() {
    const code = invitePeerIdDraft.trim();
    if (!code) {
      setError("Enter a 6-digit code");
      return;
    }
    try {
      setSaving(true);
      setError("");
      const { peer_id } = await resolveInviteCode(code);
      await handleOpenPeer(peer_id);
      setInvitePeerIdDraft("");
      setShowNewContactPopover(false);
    } catch (err) {
      setError(describeError(err, "Failed to resolve invite code"));
    } finally {
      setSaving(false);
    }
  }

  async function openManualContactPopover() {
    setContactForm(buildEmptyContact());
    setInvitePeerIdDraft("");
    setInviteCode(null);
    setShowNewContactPopover(true);
    try {
      const invite = await loadInviteCode();
      setInviteCode(invite);
    } catch {
      setInviteCode({ code: "Unavailable", peer_id: "", expires_at: 0 });
    }
  }

  function prefillFromCurrentPeer() {
    setContactForm(buildContactDraft(selectedChat, selectedAddr));
    setShowNewContactPopover(true);
  }

  function updateSelfEmoji(nextEmoji: string) {
    setSelfEmoji(nextEmoji);
    writeStorage("syne.self_emoji", nextEmoji);
    setEmojiPickerTarget(null);
  }

  function updatePeerEmoji(nextEmoji: string) {
    if (!selectedChat) {
      return;
    }
    const nextMap = {
      ...peerEmojis,
      [selectedChat.peer_id]: nextEmoji,
    };
    setPeerEmojis(nextMap);
    writeStorage("syne.peer_emojis", nextMap);
    setEmojiPickerTarget(null);
  }

  return (
    <>
      <div className="app-shell">
        <aside className="rail rail-left">
          <section className="profile-strip">
            <div className="emoji-anchor">
              <button
                type="button"
                className="avatar-badge avatar-button"
                onClick={() => setEmojiPickerTarget((current) => (current === "self" ? null : "self"))}
              >
                {selfEmoji}
              </button>
              {emojiPickerTarget === "self" ? (
                <div className="emoji-popover emoji-popover-left scrollable-emoji">
                  {EMOJI_OPTIONS.map((emoji) => (
                    <button
                      key={emoji}
                      type="button"
                      className="emoji-option"
                      onClick={() => updateSelfEmoji(emoji)}
                    >
                      {emoji}
                    </button>
                  ))}
                </div>
              ) : null}
            </div>
            <div className="profile-copy-block">
              <p
                className="eyebrow"
                title="Click to copy your Peer ID"
                style={{ cursor: "pointer", display: "inline-block", marginBottom: "4px" }}
                onClick={() => {
                  if (snapshot.local_id) navigator.clipboard.writeText(snapshot.local_id);
                }}
              >
                You
              </p>
              <p>{snapshot.chats.length} chats · {snapshot.neighbors.length} nearby peers</p>
            </div>
          </section>

          <section className="panel panel-fill">
            <div className="panel-head">
              <h2>Chats</h2>
              <span>{snapshot.chats.length}</span>
            </div>
            <input
              className="search-input"
              placeholder="Filter by title or peer id"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
            />
            <div className="chat-list">
              {filteredChats.map((chat) => (
                <button
                  key={chat.chat_id}
                  className={`chat-row ${chat.chat_id === selectedChatId ? "active" : ""}`}
                  onClick={() => setSelectedChatId(chat.chat_id)}
                >
                  <div className="chat-row-head">
                    <strong>{chat.title || chat.peer_id}</strong>
                    <time>{formatTime(chat.last_timestamp)}</time>
                  </div>
                  <div className="chat-row-body">
                    <span>{chat.preview || chat.peer_id}</span>
                    {chat.unread_count > 0 ? (
                      <em>{chat.unread_count}</em>
                    ) : chat.online ? (
                      <i>live</i>
                    ) : null}
                  </div>
                </button>
              ))}
              {!filteredChats.length ? <div className="empty-state compact">No chats yet.</div> : null}
            </div>
          </section>

          <section className="panel panel-fill">
            <div className="panel-head">
              <h2>Nearby peers</h2>
              <span>{snapshot.neighbors.length}</span>
            </div>
            <div className="neighbor-list">
              {snapshot.neighbors.map((peer) => (
                <button
                  key={peer.peer_id}
                  className="neighbor-row"
                  onClick={() => handleOpenPeer(peer.peer_id, peer.addr, peer.name || peer.peer_id)}
                >
                  <strong>{peer.name || peer.peer_id}</strong>
                  <span>{peer.addr}</span>
                </button>
              ))}
              {!snapshot.neighbors.length ? (
                <div className="empty-state compact">Waiting for LAN peers.</div>
              ) : null}
            </div>
          </section>

          <section className="contact-launcher">
            <button type="button" className="new-contact-button" onClick={openManualContactPopover}>
              New contact
            </button>
            {showNewContactPopover ? (
              <div className="contact-popover">
                <div className="panel-head popover-head">
                  <h2>Add friend</h2>
                  <button type="button" className="ghost-tiny" onClick={() => setShowNewContactPopover(false)}>
                    Close
                  </button>
                </div>
                <div className="popover-form">
                  <label>
                    <span>Your 6-digit code</span>
                    <input value={inviteCode?.code ?? "Loading..."} readOnly />
                  </label>
                  <label>
                    <span>Friend code</span>
                    <input
                      value={invitePeerIdDraft}
                      placeholder="123456"
                      onChange={(event) => setInvitePeerIdDraft(event.target.value.replace(/\D/g, "").slice(0, 6))}
                      onKeyDown={(event) => {
                        if (event.key === "Enter") {
                          event.preventDefault();
                          void handleOpenInvitePeer();
                        }
                      }}
                    />
                  </label>
                </div>
                <div className="popover-form">
                  <label>
                    <span>Display Name</span>
                    <input
                      value={contactForm.name}
                      onChange={(event) => setContactForm((current) => ({ ...current, name: event.target.value }))}
                    />
                  </label>
                  <label>
                    <span>IP</span>
                    <input
                      value={contactForm.ip}
                      onChange={(event) => setContactForm((current) => ({ ...current, ip: event.target.value }))}
                    />
                  </label>
                  <label>
                    <span>Peer ID</span>
                    <input
                      value={contactForm.peer_id}
                      onChange={(event) => setContactForm((current) => ({ ...current, peer_id: event.target.value }))}
                    />
                  </label>
                  <label>
                    <span>Port</span>
                    <input
                      value={contactForm.port}
                      onChange={(event) => setContactForm((current) => ({ ...current, port: event.target.value }))}
                    />
                  </label>
                </div>
                <div className="action-row">
                  <button type="button" className="ghost" disabled={!invitePeerIdDraft || saving} onClick={() => void handleOpenInvitePeer()}>
                    Open by code
                  </button>
                  <button type="button" className="ghost" disabled={!selectedChat} onClick={prefillFromCurrentPeer}>
                    Use current peer
                  </button>
                  <button type="button" disabled={saving} onClick={() => void handleSaveContact()}>
                    Save contact
                  </button>
                </div>
              </div>
            ) : null}
          </section>
        </aside>

        <main className="stage">
          <section className="stage-topbar">
            <div>
              <p className="eyebrow">Conversation</p>
              <h2>{selectedChat?.title ?? "Select a chat"}</h2>
            </div>
            {selectedChat ? (
              <div className="topbar-badges">
                <span className={selectedChat.online ? "pill online" : "pill"}>
                  {selectedChat.online ? "online" : "known peer"}
                </span>
                <span className={selectedChat.blocked ? "pill danger" : "pill"}>
                  {selectedChat.blocked ? "blocked" : "mesh peer"}
                </span>
              </div>
            ) : null}
          </section>

          <section className="conversation">
            {loading ? (
              <div className="empty-state">
                <h3>Connecting to backend</h3>
                <p>The Tauri shell expects the Go bridge at {getApiBase()}.</p>
              </div>
            ) : selectedChat ? (
              <>
                <div className="message-stream">
                  {selectedMessages.map((message) => (
                    <article
                      key={message.message_id ?? `${message.timestamp}-${message.from}`}
                      className={`bubble ${message.direction}`}
                    >
                      <header>
                        <strong>{message.direction === "outgoing" ? "You" : selectedChat.title || message.from}</strong>
                        <div className="message-meta">
                          <span className={`delivery-pill ${message.strategy}`}>{message.strategy}</span>
                          <time>{formatTime(message.timestamp)}</time>
                        </div>
                      </header>
                      <p>{message.text}</p>
                    </article>
                  ))}
                  {!selectedMessages.length ? (
                    <div className="empty-state inset">
                      <h3>Chat is open</h3>
                      <p>Handshake is ready. Send the first message.</p>
                    </div>
                  ) : null}
                </div>
                <footer className="composer">
                  <textarea
                    placeholder="Type a message"
                    value={composer}
                    onChange={(event) => setComposer(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === "Enter" && !event.shiftKey) {
                        event.preventDefault();
                        void handleSend();
                      }
                    }}
                  />
                  <button onClick={() => void handleSend()}>Send</button>
                </footer>
              </>
            ) : (
              <div className="empty-state">
                <h3>No active conversation</h3>
                <p>Pick a chat or open a nearby peer from the left rail.</p>
              </div>
            )}
          </section>
        </main>

        <aside className="rail rail-right">
          {selectedChat ? (
            <section className="panel peer-panel panel-fill discord-profile-card">
              {/* Верхняя декоративная плашка */}
              <div className="profile-banner"></div>

              <div className="peer-hero">
                <div className="emoji-anchor">
                  <button
                    type="button"
                    className="avatar-large avatar-button profile-avatar"
                    onClick={() => setEmojiPickerTarget((current) => (current === "peer" ? null : "peer"))}
                  >
                    {selectedPeerEmoji}
                    <div className={`status-badge ${selectedChat.online ? "online" : ""}`}></div>
                  </button>

                  {emojiPickerTarget === "peer" ? (
                    <div className="emoji-popover scrollable-emoji">
                      {EMOJI_OPTIONS.map((emoji) => (
                        <button key={emoji} type="button" className="emoji-option" onClick={() => updatePeerEmoji(emoji)}>
                          {emoji}
                        </button>
                      ))}
                    </div>
                  ) : null}
                </div>

                <div className="peer-title-block">
                  {editingPeerName ? (
                    <input
                      className="inline-name-input"
                      value={peerNameDraft}
                      autoFocus
                      onChange={(event) => setPeerNameDraft(event.target.value)}
                      onBlur={() => void handleCommitPeerName()}
                      onKeyDown={(event) => {
                        if (event.key === "Enter") {
                          event.preventDefault();
                          void handleCommitPeerName();
                        }
                        if (event.key === "Escape") {
                          setEditingPeerName(false);
                          setPeerNameDraft(selectedContact?.name ?? selectedChat.title);
                        }
                      }}
                    />
                  ) : (
                    <button type="button" className="editable-name" onClick={() => setEditingPeerName(true)}>
                      {selectedChat.title}
                    </button>
                  )}
                  <p className="peer-id-subtextline">@{selectedChat.peer_id.slice(0, 8)}...</p>
                </div>
              </div>

              <div className="profile-content-scrollable">
                <div className="profile-divider"></div>

                {/* Секция с кнопкой действий (бывшая Update/Save) */}
                {!selectedContact && (
                  <button className="primary-action-btn" onClick={prefillFromCurrentPeer}>
                    Add to Contacts
                  </button>
                )}

                <div className="info-section">
                  <span className="section-label">PEER ID</span>
                  <div className="id-copy-box" onClick={() => navigator.clipboard.writeText(selectedChat.peer_id)}>
                    <code>{selectedChat.peer_id}</code>
                  </div>
                </div>
              </div>

              {/* Блок блокировки примагничен к низу карточки */}
              <div className="block-box-footer">
                <div className="inline-block-form">
                  <input
                    placeholder="Reason..."
                    value={blockReason}
                    onChange={(event) => setBlockReason(event.target.value)}
                  />
                  <button className="danger-icon-btn" title="Block Peer" onClick={() => void handleBlock()}>
                    Block
                  </button>
                </div>
              </div>
            </section>
          ) : (
            <div className="empty-state peer-empty">
              <h3>No peer selected</h3>
              <p>Select a chat to view profile.</p>
            </div>
          )}

          {/* Карточка заблокированных с фиксированной высотой и прокруткой */}
          <section className="panel blocked-panel scrollable-panel">
            <div className="panel-head">
              <h2>Blocked</h2>
              <span>{snapshot.blocked.length}</span>
            </div>
            <div className="blocked-list scroll-area">
              {snapshot.blocked.map((item) => (
                <div key={item.peer_id} className="blocked-item">
                  <div className="blocked-info">
                    <strong>{item.name || "Unknown"}</strong>
                    <span>{item.reason || "No reason"}</span>
                  </div>
                  <button className="ghost-tiny" onClick={() => void handleUnblock(item.peer_id)}>
                    Unblock
                  </button>
                </div>
              ))}
              {!snapshot.blocked.length ? <div className="empty-state compact">Clear</div> : null}
            </div>
          </section>

        </aside>
      </div>

      {error ? (
        <div
          key={errorToastKey}
          className="error-toast"
          role="alert"
          aria-live="assertive"
        >
          <span className="error-toast-title">Error</span>
          <p>{error}</p>
        </div>
      ) : null}
    </>
  );
}
