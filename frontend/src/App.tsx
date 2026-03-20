import {
  startTransition,
  useDeferredValue,
  useEffect,
  useState,
} from "react";
import {
  blockPeer,
  deleteContact,
  getApiBase,
  listenEvents,
  loadBootstrap,
  loadMessages,
  markChatRead,
  openPrivateChat,
  renameContact,
  saveContact,
  sendMessage,
  unblockPeer,
} from "./lib/api";
import type {
  AppEvent,
  ChatSummary,
  Contact,
  Snapshot,
  UIMessage,
} from "./types";

const EMPTY_SNAPSHOT: Snapshot = {
  local_id: "",
  port: 0,
  contacts: [],
  blocked: [],
  neighbors: [],
  chats: [],
};

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
  const index = addr.lastIndexOf(":");
  if (index === -1) {
    return { ip: addr, port: "" };
  }
  return {
    ip: addr.slice(0, index),
    port: addr.slice(index + 1),
  };
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

export default function App() {
  const [snapshot, setSnapshot] = useState<Snapshot>(EMPTY_SNAPSHOT);
  const [selectedChatId, setSelectedChatId] = useState("");
  const [messages, setMessages] = useState<Record<string, UIMessage[]>>({});
  const [query, setQuery] = useState("");
  const [composer, setComposer] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [contactForm, setContactForm] = useState<Contact>({
    name: "",
    peer_id: "",
    ip: "",
    port: "",
  });
  const [renameValue, setRenameValue] = useState("");
  const [blockReason, setBlockReason] = useState("");
  const deferredQuery = useDeferredValue(query);

  const selectedChat = snapshot.chats.find((item) => item.chat_id === selectedChatId) ?? null;
  const selectedMessages = selectedChat ? messages[selectedChat.chat_id] ?? [] : [];
  const filteredChats = snapshot.chats.filter((item) => {
    const needle = deferredQuery.trim().toLowerCase();
    if (!needle) {
      return true;
    }
    return (
      item.title.toLowerCase().includes(needle) ||
      item.peer_id.toLowerCase().includes(needle)
    );
  });

  async function refreshBootstrap(preserveSelection = true) {
    const nextSnapshot = await loadBootstrap();
    startTransition(() => {
      setSnapshot(nextSnapshot);
      if (!preserveSelection) {
        setSelectedChatId(nextSnapshot.chats[0]?.chat_id ?? "");
        return;
      }
      if (selectedChatId) {
        const exists = nextSnapshot.chats.some(
          (item) => item.chat_id === selectedChatId,
        );
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
        if (!mounted) {
          return;
        }
        setError(err instanceof Error ? err.message : "Failed to reach backend");
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
    const stop = listenEvents((event: AppEvent) => {
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
              [event.message!.chat_id]: nextItems.sort(
                (a, b) => a.timestamp - b.timestamp,
              ),
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

      if (
        event.type === "peer_discovered" ||
        event.type === "contact_added" ||
        event.type === "contact_updated" ||
        event.type === "contact_deleted" ||
        event.type === "peer_blocked" ||
        event.type === "peer_unblocked"
      ) {
        void refreshBootstrap();
      }
    });

    return () => {
      stop();
    };
  }, []);

  useEffect(() => {
    if (!selectedChat) {
      setContactForm({ name: "", peer_id: "", ip: "", port: "" });
      setRenameValue("");
      setBlockReason("");
      return;
    }
    const contact = snapshot.contacts.find(
      (item) => item.peer_id === selectedChat.peer_id,
    );
    const { ip, port } = splitAddress(selectedChat.known_addr);
    setContactForm({
      name: contact?.name ?? selectedChat.title,
      peer_id: selectedChat.peer_id,
      ip: contact?.ip ?? ip,
      port: contact?.port ?? port,
    });
    setRenameValue(contact?.name ?? selectedChat.title);
  }, [selectedChat, snapshot.contacts]);

  async function handleOpenPeer(peerId: string, peerAddr?: string, name?: string) {
    try {
      setError("");
      const chat = await openPrivateChat({
        peer_id: peerId,
        peer_addr: peerAddr,
        name,
      });
      startTransition(() => {
        setSnapshot((current) => ({
          ...current,
          chats: upsertChat(current.chats, chat),
        }));
        setSelectedChatId(chat.chat_id);
      });
      await refreshMessages(chat.chat_id);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to open chat");
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
      setError(err instanceof Error ? err.message : "Failed to send message");
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
      const saved = await saveContact(contactForm);
      await handleOpenPeer(
        saved.peer_id,
        `${saved.ip}:${saved.port}`,
        saved.name,
      );
      await refreshBootstrap();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to save contact");
    } finally {
      setSaving(false);
    }
  }

  async function handleRename() {
    if (!selectedChat || !renameValue.trim()) {
      return;
    }
    try {
      setSaving(true);
      setError("");
      await renameContact(selectedChat.peer_id, renameValue.trim());
      await refreshBootstrap();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to rename contact");
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
      setError(err instanceof Error ? err.message : "Failed to delete contact");
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
      await refreshBootstrap();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to block peer");
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
      setError(err instanceof Error ? err.message : "Failed to unblock peer");
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="app-shell">
      <aside className="rail rail-left">
        <section className="brand-card">
          <p className="eyebrow">P2P messenger</p>
          <h1>Syne</h1>
          <p className="brand-copy">
            Desktop shell over the local Go core. No relay, no cloud, just LAN.
          </p>
          <dl className="brand-meta">
            <div>
              <dt>Peer</dt>
              <dd>{snapshot.local_id || "pending"}</dd>
            </div>
            <div>
              <dt>Port</dt>
              <dd>{snapshot.port || "..."}</dd>
            </div>
            <div>
              <dt>Bridge</dt>
              <dd>{getApiBase()}</dd>
            </div>
          </dl>
        </section>

        <section className="panel">
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
            {!filteredChats.length ? (
              <div className="empty-state compact">No chats yet.</div>
            ) : null}
          </div>
        </section>

        <section className="panel">
          <div className="panel-head">
            <h2>Radar</h2>
            <span>{snapshot.neighbors.length}</span>
          </div>
          <div className="neighbor-list">
            {snapshot.neighbors.map((peer) => (
              <button
                key={peer.peer_id}
                className="neighbor-row"
                onClick={() =>
                  handleOpenPeer(peer.peer_id, peer.addr, peer.name || peer.peer_id)
                }
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
                {selectedChat.blocked ? "blocked" : selectedChat.peer_id}
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
                      <strong>
                        {message.direction === "outgoing" ? "You" : message.from}
                      </strong>
                      <time>{formatTime(message.timestamp)}</time>
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
              <p>Pick a contact or a discovered peer from the left rail.</p>
            </div>
          )}
        </section>
      </main>

      <aside className="rail rail-right">
        <section className="panel accent">
          <div className="panel-head">
            <h2>Contact card</h2>
            <span>{selectedChat?.peer_id ?? "new"}</span>
          </div>
          <div className="form-grid">
            <label>
              <span>Name</span>
              <input
                value={contactForm.name}
                onChange={(event) =>
                  setContactForm((current) => ({
                    ...current,
                    name: event.target.value,
                  }))
                }
              />
            </label>
            <label>
              <span>Peer ID</span>
              <input
                value={contactForm.peer_id}
                onChange={(event) =>
                  setContactForm((current) => ({
                    ...current,
                    peer_id: event.target.value,
                  }))
                }
              />
            </label>
            <label>
              <span>IP</span>
              <input
                value={contactForm.ip}
                onChange={(event) =>
                  setContactForm((current) => ({
                    ...current,
                    ip: event.target.value,
                  }))
                }
              />
            </label>
            <label>
              <span>Port</span>
              <input
                value={contactForm.port}
                onChange={(event) =>
                  setContactForm((current) => ({
                    ...current,
                    port: event.target.value,
                  }))
                }
              />
            </label>
          </div>
          <div className="button-row">
            <button disabled={saving} onClick={() => void handleSaveContact()}>
              Save contact
            </button>
            <button
              className="ghost"
              disabled={saving || !selectedChat}
              onClick={() => void handleDeleteContact()}
            >
              Delete
            </button>
          </div>
        </section>

        <section className="panel">
          <div className="panel-head">
            <h2>Rename</h2>
            <span>local alias</span>
          </div>
          <div className="inline-form">
            <input
              placeholder="Visible name"
              value={renameValue}
              onChange={(event) => setRenameValue(event.target.value)}
            />
            <button disabled={saving || !selectedChat} onClick={() => void handleRename()}>
              Update
            </button>
          </div>
        </section>

        <section className="panel warning">
          <div className="panel-head">
            <h2>Blocklist</h2>
            <span>{snapshot.blocked.length}</span>
          </div>
          <div className="inline-form">
            <input
              placeholder="Reason"
              value={blockReason}
              onChange={(event) => setBlockReason(event.target.value)}
            />
            <button
              className="danger"
              disabled={saving || !selectedChat}
              onClick={() => void handleBlock()}
            >
              Block peer
            </button>
          </div>
          <div className="blocked-list">
            {snapshot.blocked.map((item) => (
              <div key={item.peer_id} className="blocked-row">
                <div>
                  <strong>{item.name || item.peer_id}</strong>
                  <span>{item.reason || "no reason"}</span>
                </div>
                <button className="ghost" onClick={() => void handleUnblock(item.peer_id)}>
                  Unblock
                </button>
              </div>
            ))}
            {!snapshot.blocked.length ? (
              <div className="empty-state compact">Blocklist is empty.</div>
            ) : null}
          </div>
        </section>

        {error ? (
          <section className="panel error-panel">
            <div className="panel-head">
              <h2>Backend</h2>
              <span>attention</span>
            </div>
            <p>{error}</p>
          </section>
        ) : null}
      </aside>
    </div>
  );
}
