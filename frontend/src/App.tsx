import {
  startTransition,
  useDeferredValue,
  useEffect,
  useMemo,
  useRef,
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
import { Image as TauriImage } from "@tauri-apps/api/image";
import { getCurrentWindow } from "@tauri-apps/api/window";
import blackIcon from "../src-tauri/icons/black.jpeg";
import blueIcon from "../src-tauri/icons/blue.jpeg";
import greenIcon from "../src-tauri/icons/green.jpeg";
import orangeIcon from "../src-tauri/icons/orange.jpeg";
import redIcon from "../src-tauri/icons/red.jpeg";
import skyIcon from "../src-tauri/icons/sky.jpeg";
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
type SidebarView = "chats" | "contacts" | "network" | "blocked";
type ThemePreference = "system" | "light" | "dark";
const SETTINGS_SECTIONS = [
  { id: "notifications", label: "🔔 Notifications" },
  { id: "language", label: "🌐 Language" },
  { id: "appearance", label: "🎨 Appearance" },
  { id: "delete-history", label: "🗑️ Delete chat history" },
  { id: "sync-devices", label: "📱 Sync devices" },
  { id: "device-key", label: "🔐 Device key" },
  { id: "system-settings", label: "⚙️ System settings" },
] as const;
type SettingsSectionId = (typeof SETTINGS_SECTIONS)[number]["id"];
const THEME_OPTIONS: Array<{ id: ThemePreference; label: string }> = [
  { id: "light", label: "Light" },
  { id: "dark", label: "Dark" },
  { id: "system", label: "System" },
];
const APP_ICON_OPTIONS = [
  { id: "blue", label: "Blue", src: blueIcon },
  { id: "green", label: "Green", src: greenIcon },
  { id: "orange", label: "Orange", src: orangeIcon },
  { id: "red", label: "Red", src: redIcon },
  { id: "sky", label: "Sky", src: skyIcon },
  { id: "black", label: "Black", src: blackIcon },
] as const;
type AppIconId = (typeof APP_ICON_OPTIONS)[number]["id"];

function formatTime(value: number) {
  if (!value) {
    return "";
  }
  return new Intl.DateTimeFormat([], {
    hour: "2-digit",
    minute: "2-digit",
  }).format(value);
}

function formatDate(value: number) {
  if (!value) return "";
  return new Intl.DateTimeFormat([], {
    month: "short",
    day: "numeric",
  }).format(value).toUpperCase();
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

function joinAddress(ip?: string, port?: string) {
  const cleanIP = (ip ?? "").trim();
  const cleanPort = (port ?? "").trim();
  if (!cleanIP) {
    return "";
  }
  if (!cleanPort) {
    return cleanIP;
  }
  if (cleanIP.includes(":") && !cleanIP.startsWith("[") && !cleanIP.endsWith("]")) {
    return `[${cleanIP}]:${cleanPort}`;
  }
  return `${cleanIP}:${cleanPort}`;
}

async function buildWindowIcon(src: string) {
  if (typeof window === "undefined" || typeof document === "undefined") {
    return null;
  }

  const image = await new Promise<HTMLImageElement>((resolve, reject) => {
    const nextImage = new Image();
    nextImage.onload = () => resolve(nextImage);
    nextImage.onerror = () => reject(new Error(`Failed to load icon: ${src}`));
    nextImage.src = src;
  });

  const width = image.naturalWidth || image.width;
  const height = image.naturalHeight || image.height;
  if (!width || !height) {
    throw new Error("Selected icon has invalid dimensions");
  }

  const canvas = document.createElement("canvas");
  canvas.width = width;
  canvas.height = height;

  const ctx = canvas.getContext("2d");
  if (!ctx) {
    throw new Error("Canvas 2D context is unavailable");
  }

  ctx.drawImage(image, 0, 0, width, height);
  const rgba = new Uint8Array(ctx.getImageData(0, 0, width, height).data);
  return TauriImage.new(rgba, width, height);
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

function getInitial(name: string): string {
  if (!name) return "?";
  const cleaned = name.replace(/^@/, "").trim();
  return cleaned.charAt(0).toUpperCase();
}

export default function App() {
  const [snapshot, setSnapshot] = useState<Snapshot>(EMPTY_SNAPSHOT);
  const [selectedChatId, setSelectedChatId] = useState("");
  const [messages, setMessages] = useState<Record<string, UIMessage[]>>({});
  const [hiddenChatIds, setHiddenChatIds] = useState<string[]>(() =>
    readStorage("syne.hidden_chat_ids", []),
  );
  const [chatContextMenu, setChatContextMenu] = useState<{
    chatId: string;
    x: number;
    y: number;
  } | null>(null);
  const [contactContextMenu, setContactContextMenu] = useState<{
    peerId: string;
    x: number;
    y: number;
  } | null>(null);
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
  const [showDetailPanel, setShowDetailPanel] = useState(false);
  const [sidebarView, setSidebarView] = useState<SidebarView>("chats");
  const [showSettings, setShowSettings] = useState(false);
  const [activeSettingsSection, setActiveSettingsSection] = useState<SettingsSectionId | null>(null);
  const [themePreference, setThemePreference] = useState<ThemePreference>(() =>
    readStorage("syne.theme_preference", "system"),
  );
  const [selectedAppIcon, setSelectedAppIcon] = useState<AppIconId>(() => {
    const stored = readStorage<string>("syne.app_icon", "blue");
    return APP_ICON_OPTIONS.some((item) => item.id === stored)
      ? stored as AppIconId
      : "blue";
  });
  const [systemTheme, setSystemTheme] = useState<"light" | "dark">(() => {
    if (typeof window === "undefined") {
      return "dark";
    }
    return window.matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark";
  });
  const [emojiPickerTarget, setEmojiPickerTarget] = useState<"self" | "peer" | null>(null);
  const [selfEmoji, setSelfEmoji] = useState(() => readStorage("syne.self_emoji", "🙂"));
  const [peerEmojis, setPeerEmojis] = useState<Record<string, string>>(() =>
    readStorage("syne.peer_emojis", {}),
  );
  const messageStreamRef = useRef<HTMLDivElement | null>(null);
  const pendingScrollBehaviorRef = useRef<ScrollBehavior | null>(null);
  const deferredQuery = useDeferredValue(query);
  const visibleChats = useMemo(
    () => snapshot.chats.filter((item) => !hiddenChatIds.includes(item.chat_id)),
    [hiddenChatIds, snapshot.chats],
  );

  const selectedChat = visibleChats.find((item) => item.chat_id === selectedChatId) ?? null;
  const selectedContact = selectedChat
    ? snapshot.contacts.find((item) => item.peer_id === selectedChat.peer_id) ?? null
    : null;
  const selectedPeer = selectedChat
    ? snapshot.neighbors.find((item) => item.peer_id === selectedChat.peer_id) ?? null
    : null;
  const selectedMessages = selectedChat ? messages[selectedChat.chat_id] ?? [] : [];
  const selectedAddr = selectedChat?.known_addr || selectedPeer?.addr || "";
  const selectedPeerEmoji = selectedChat
    ? peerEmojis[selectedChat.peer_id] ?? getInitial(selectedChat.title || selectedChat.peer_id)
    : "🙂";
  const activeSettingsItem = activeSettingsSection
    ? SETTINGS_SECTIONS.find((item) => item.id === activeSettingsSection) ?? null
    : null;
  const resolvedTheme = themePreference === "system" ? systemTheme : themePreference;
  const currentAppIcon = APP_ICON_OPTIONS.find((item) => item.id === selectedAppIcon) ?? APP_ICON_OPTIONS[0];

  const filteredChats = useMemo(() => {
    const needle = deferredQuery.trim().toLowerCase();
    if (!needle) {
      return visibleChats;
    }
    return visibleChats.filter((item) => (
      item.title.toLowerCase().includes(needle) ||
      item.peer_id.toLowerCase().includes(needle)
    ));
  }, [deferredQuery, visibleChats]);

  const filteredNearbyPeers = useMemo(() => {
    const needle = deferredQuery.trim().toLowerCase();
    if (!needle) {
      return snapshot.neighbors;
    }
    return snapshot.neighbors.filter((item) => (
      (item.name || "").toLowerCase().includes(needle) ||
      item.peer_id.toLowerCase().includes(needle) ||
      item.addr.toLowerCase().includes(needle)
    ));
  }, [deferredQuery, snapshot.neighbors]);

  const filteredContacts = useMemo(() => {
    const needle = deferredQuery.trim().toLowerCase();
    if (!needle) {
      return snapshot.contacts;
    }
    return snapshot.contacts.filter((item) => (
      item.name.toLowerCase().includes(needle) ||
      item.peer_id.toLowerCase().includes(needle) ||
      item.ip.toLowerCase().includes(needle) ||
      item.port.toLowerCase().includes(needle)
    ));
  }, [deferredQuery, snapshot.contacts]);

  const filteredBlockedPeers = useMemo(() => {
    const needle = deferredQuery.trim().toLowerCase();
    if (!needle) {
      return snapshot.blocked;
    }
    return snapshot.blocked.filter((item) => (
      (item.name || "").toLowerCase().includes(needle) ||
      item.peer_id.toLowerCase().includes(needle) ||
      (item.reason || "").toLowerCase().includes(needle)
    ));
  }, [deferredQuery, snapshot.blocked]);

  const sidebarTitle = sidebarView === "chats"
    ? "Chats"
    : sidebarView === "contacts"
      ? "Contacts"
    : sidebarView === "network"
      ? "Nearby"
      : "Black list";

  const sidebarBadge = sidebarView === "chats"
    ? `${filteredChats.length}`
    : sidebarView === "contacts"
      ? `${filteredContacts.length}`
    : sidebarView === "network"
      ? `${filteredNearbyPeers.length}`
      : `${filteredBlockedPeers.length}`;

  const searchPlaceholder = sidebarView === "chats"
    ? "Search chats..."
    : sidebarView === "contacts"
      ? "Search contacts..."
    : sidebarView === "network"
      ? "Search nearby peers..."
      : "Search blocked peers...";

  async function refreshBootstrap(preserveSelection = true) {
    const nextSnapshot = await loadBootstrap();
    const nextVisibleChats = nextSnapshot.chats.filter((item) => !hiddenChatIds.includes(item.chat_id));
    startTransition(() => {
      setSnapshot(nextSnapshot);
      if (!preserveSelection) {
        setSelectedChatId(nextVisibleChats[0]?.chat_id ?? "");
        return;
      }
      if (selectedChatId) {
        const exists = nextVisibleChats.some((item) => item.chat_id === selectedChatId);
        if (!exists) {
          setSelectedChatId(nextVisibleChats[0]?.chat_id ?? "");
        }
      } else {
        setSelectedChatId(nextVisibleChats[0]?.chat_id ?? "");
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
    const handleContextMenu = (event: MouseEvent) => {
      event.preventDefault();
    };

    const handlePointerDown = () => {
      setChatContextMenu(null);
      setContactContextMenu(null);
    };

    const handleKeyDown = (event: KeyboardEvent) => {
      const key = event.key.toLowerCase();
      const modifier = event.ctrlKey || event.metaKey;

      if (key === "escape") {
        setChatContextMenu(null);
      }

      if (key === "f12") {
        event.preventDefault();
        return;
      }

      if (modifier && key === "u") {
        event.preventDefault();
        return;
      }

      if (modifier && event.shiftKey && (key === "i" || key === "j" || key === "c")) {
        event.preventDefault();
        return;
      }

      if (event.metaKey && event.altKey && (key === "i" || key === "j" || key === "c")) {
        event.preventDefault();
      }
    };

    window.addEventListener("contextmenu", handleContextMenu);
    window.addEventListener("pointerdown", handlePointerDown);
    window.addEventListener("keydown", handleKeyDown);
    window.addEventListener("scroll", handlePointerDown, true);

    return () => {
      window.removeEventListener("contextmenu", handleContextMenu);
      window.removeEventListener("pointerdown", handlePointerDown);
      window.removeEventListener("keydown", handleKeyDown);
      window.removeEventListener("scroll", handlePointerDown, true);
    };
  }, []);

  useEffect(() => {
    if (!selectedChatId) {
      return;
    }
    pendingScrollBehaviorRef.current = "auto";
    void refreshMessages(selectedChatId);
    void markChatRead(selectedChatId).catch(() => undefined);
  }, [selectedChatId]);

  useEffect(() => {
    const behavior = pendingScrollBehaviorRef.current;
    if (!behavior) {
      return;
    }
    const node = messageStreamRef.current;
    if (!node) {
      return;
    }
    pendingScrollBehaviorRef.current = null;
    window.requestAnimationFrame(() => {
      node.scrollTo({
        top: node.scrollHeight,
        behavior,
      });
    });
  }, [selectedMessages, selectedChatId]);

  useEffect(() => {
    if (!selectedChatId) {
      return;
    }
    const exists = visibleChats.some((item) => item.chat_id === selectedChatId);
    if (!exists) {
      setSelectedChatId(visibleChats[0]?.chat_id ?? "");
    }
  }, [selectedChatId, visibleChats]);

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
    if (typeof window === "undefined") {
      return;
    }
    const media = window.matchMedia("(prefers-color-scheme: light)");
    const handleChange = (event: MediaQueryListEvent) => {
      setSystemTheme(event.matches ? "light" : "dark");
    };

    setSystemTheme(media.matches ? "light" : "dark");
    if (typeof media.addEventListener === "function") {
      media.addEventListener("change", handleChange);
      return () => media.removeEventListener("change", handleChange);
    }
    media.addListener(handleChange);
    return () => media.removeListener(handleChange);
  }, []);

  useEffect(() => {
    writeStorage("syne.theme_preference", themePreference);
  }, [themePreference]);

  useEffect(() => {
    writeStorage("syne.app_icon", selectedAppIcon);
  }, [selectedAppIcon]);

  useEffect(() => {
    if (typeof document === "undefined") {
      return;
    }
    document.documentElement.dataset.theme = resolvedTheme;
    document.documentElement.style.colorScheme = resolvedTheme;
    const themeMeta = document.querySelector('meta[name="theme-color"]');
    themeMeta?.setAttribute("content", resolvedTheme === "light" ? "#f3ebdd" : "#0e1117");
  }, [resolvedTheme]);

  useEffect(() => {
    if (typeof document === "undefined") {
      return;
    }
    let iconLink = document.querySelector("link[rel='icon']") as HTMLLinkElement | null;
    if (!iconLink) {
      iconLink = document.createElement("link");
      iconLink.rel = "icon";
      document.head.appendChild(iconLink);
    }
    iconLink.href = currentAppIcon.src;
  }, [currentAppIcon.src]);

  useEffect(() => {
    if (typeof window === "undefined" || !("__TAURI_INTERNALS__" in window)) {
      return;
    }

    let cancelled = false;

    async function applyWindowIcon() {
      try {
        const icon = await buildWindowIcon(currentAppIcon.src);
        if (!icon || cancelled) {
          return;
        }
        await getCurrentWindow().setIcon(icon);
      } catch (err) {
        console.warn("Failed to apply runtime window icon", err);
      }
    }

    void applyWindowIcon();
    return () => {
      cancelled = true;
    };
  }, [currentAppIcon.src]);

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
    setShowDetailPanel(false);
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
      setSidebarView("chats");
      try {
        await refreshMessages(chat.chat_id);
      } catch (err) {
        setError(describeError(err, "Failed to load chat history"));
      }
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
    pendingScrollBehaviorRef.current = "smooth";
    try {
      setError("");
      await sendMessage({
        chat_id: selectedChat.chat_id,
        target_id: selectedChat.peer_id,
        text,
      });
    } catch (err) {
      pendingScrollBehaviorRef.current = null;
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

  async function handleDeleteContact(peerId?: string) {
    const targetPeerId = peerId ?? selectedChat?.peer_id;
    if (!targetPeerId) return;
    try {
      setSaving(true);
      setError("");
      await deleteContact(targetPeerId);
      setContactContextMenu(null);
      await refreshBootstrap();
    } catch (err) {
      setError(describeError(err, "Failed to delete contact"));
    } finally {
      setSaving(false);
    }
  }

  async function handleBlock() {
    if (!selectedChat) return;
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
  function handleSettings() {
    setActiveSettingsSection(null);
    setShowSettings(true);
  }

  function closeSettings() {
    setShowSettings(false);
    setActiveSettingsSection(null);
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
    if (!selectedChat) return;
    const nextMap = {
      ...peerEmojis,
      [selectedChat.peer_id]: nextEmoji,
    };
    setPeerEmojis(nextMap);
    writeStorage("syne.peer_emojis", nextMap);
    setEmojiPickerTarget(null);
  }

  function getPeerAvatar(peerId: string, label: string) {
    return peerEmojis[peerId] ?? getInitial(label);
  }

  function persistHiddenChats(nextIds: string[]) {
    setHiddenChatIds(nextIds);
    writeStorage("syne.hidden_chat_ids", nextIds);
  }

  function handleHideChat(chatId: string) {
    if (hiddenChatIds.includes(chatId)) {
      return;
    }
    persistHiddenChats([...hiddenChatIds, chatId]);
    setChatContextMenu(null);
  }

  function handleChatContextMenu(event: React.MouseEvent, chatId: string) {
    event.preventDefault();
    event.stopPropagation();
    setChatContextMenu({
      chatId,
      x: event.clientX,
      y: event.clientY,
    });
    setContactContextMenu(null);
  }

  function handleContactContextMenu(event: React.MouseEvent, peerId: string) {
    event.preventDefault();
    event.stopPropagation();
    setContactContextMenu({
      peerId,
      x: event.clientX,
      y: event.clientY,
    });
    setChatContextMenu(null);
  }

  // First message date for session separator
  const firstMsgDate = selectedMessages.length > 0
    ? formatDate(selectedMessages[0].timestamp)
    : "";

  return (
    <>
      <div className="app-shell">
        {/* ─── Icon Rail ─── */}
        <nav className="icon-rail">
          <button
            type="button"
            className="icon-rail-brand"
            onClick={() => {
              setShowSettings(true);
              setActiveSettingsSection("appearance");
            }}
            title="Appearance"
          >
            <img
              src={currentAppIcon.src}
              alt={`${currentAppIcon.label} app icon`}
              className="icon-rail-brand-image"
            />
          </button>

          <div className="emoji-anchor">
            <button
              type="button"
              className="icon-rail-avatar"
              onClick={() => setEmojiPickerTarget((c) => (c === "self" ? null : "self"))}
              title="Your avatar"
            >
              {selfEmoji}
            </button>
            {emojiPickerTarget === "self" ? (
              <div className="emoji-popover scrollable-emoji">
                {EMOJI_OPTIONS.map((emoji) => (
                  <button key={emoji} type="button" className="emoji-option" onClick={() => updateSelfEmoji(emoji)}>
                    {emoji}
                  </button>
                ))}
              </div>
            ) : null}
          </div>

          {/* Chat icon */}
          <button
            type="button"
            className={`icon-rail-btn ${sidebarView === "chats" ? "active" : ""}`}
            title="Chats"
            onClick={() => setSidebarView("chats")}
          >
            <svg viewBox="0 0 24 24" fill="currentColor"><path d="M20 2H4c-1.1 0-2 .9-2 2v18l4-4h14c1.1 0 2-.9 2-2V4c0-1.1-.9-2-2-2zm0 14H6l-2 2V4h16v12z" /></svg>
          </button>

          {/* Network icon */}
          <button
            type="button"
            className={`icon-rail-btn ${sidebarView === "network" ? "active" : ""}`}
            title="Network"
            onClick={() => setSidebarView("network")}
          >
            <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="m10.586 5.414-5.172 5.172" /><path d="m18.586 13.414-5.172 5.172" /><path d="M6 12h12" /><circle cx="12" cy="20" r="2" /><circle cx="12" cy="4" r="2" /><circle cx="20" cy="12" r="2" /><circle cx="4" cy="12" r="2" /></svg>
          </button>
          {/*Contacts icon */}
          <button
            type="button"
            className={`icon-rail-btn ${sidebarView === "contacts" ? "active" : ""}`}
            title="Contacts"
            onClick={() => setSidebarView("contacts")}
          >
            <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M16 2v2" /><path d="M7 22v-2a2 2 0 0 1 2-2h6a2 2 0 0 1 2 2v2" /><path d="M8 2v2" /><circle cx="12" cy="11" r="3" /><rect x="3" y="4" width="18" height="18" rx="2" /></svg>
          </button>
          {/* Black list icon */}
          <button
            type="button"
            className={`icon-rail-btn ${sidebarView === "blocked" ? "active" : ""}`}
            title="Black list"
            onClick={() => setSidebarView("blocked")}
          >
            <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><circle cx="12" cy="12" r="10" /><path d="M4.929 4.929 19.07 19.071" /></svg>
          </button>

          <div className="icon-rail-spacer" />
          {/*Settings icon */}
          <button
            type="button"
            className={`icon-rail-btn ${showSettings ? "active" : ""}`}
            title="Settings"
            onClick={handleSettings}
          >
            <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className="lucide lucide-settings-icon lucide-settings"><path d="M9.671 4.136a2.34 2.34 0 0 1 4.659 0 2.34 2.34 0 0 0 3.319 1.915 2.34 2.34 0 0 1 2.33 4.033 2.34 2.34 0 0 0 0 3.831 2.34 2.34 0 0 1-2.33 4.033 2.34 2.34 0 0 0-3.319 1.915 2.34 2.34 0 0 1-4.659 0 2.34 2.34 0 0 0-3.32-1.915 2.34 2.34 0 0 1-2.33-4.033 2.34 2.34 0 0 0 0-3.831A2.34 2.34 0 0 1 6.35 6.051a2.34 2.34 0 0 0 3.319-1.915" /><circle cx="12" cy="12" r="3" /></svg>
          </button>

        </nav>

        {/* ─── Peers Panel ─── */}
        <aside className="peers-panel">
          <div className="peers-panel-header">
            <h2>{sidebarTitle}</h2>
            <span className="live-badge">{sidebarBadge}</span>
          </div>

          <div className="peers-panel-search">
            <input
              placeholder={searchPlaceholder}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
          </div>

          <div className="peer-list">
            {sidebarView === "chats" ? (
              <>
                {filteredChats.map((chat) => (
                  <div
                    key={chat.chat_id}
                    className="chat-list-row"
                  >
                    <button
                      className={`peer-card ${chat.chat_id === selectedChatId ? "active" : ""}`}
                      onContextMenu={(event) => handleChatContextMenu(event, chat.chat_id)}
                      onClick={() => {
                        setChatContextMenu(null);
                        setSelectedChatId(chat.chat_id);
                      }}
                    >
                      <div className={`peer-card-avatar ${chat.blocked ? "blocked" : chat.online ? "online" : ""}`}>
                        {getPeerAvatar(chat.peer_id, chat.title || chat.peer_id)}
                      </div>
                      <div className="peer-card-info">
                        <div className="peer-card-info-top">
                          <strong>{chat.title || chat.peer_id}</strong>
                          <time>{formatTime(chat.last_timestamp)}</time>
                        </div>
                        <div className="peer-card-info-bottom">
                          <span>{chat.preview || (chat.online ? "Encrypted Stream" : "Last seen: recently")}</span>
                          {chat.unread_count > 0 ? (
                            <span className="unread-badge">{chat.unread_count}</span>
                          ) : chat.blocked ? (
                            <span className="live-tag blocked">blocked</span>
                          ) : chat.online ? (
                            <span className="live-tag">live</span>
                          ) : (
                            <span className="offline-tag">offline</span>
                          )}
                        </div>
                      </div>
                    </button>
                  </div>
                ))}
                {!filteredChats.length ? (
                  <div className="empty-state compact">No chats yet.</div>
                ) : null}
              </>
            ) : null}

            {sidebarView === "network" ? (
              <>
                {filteredNearbyPeers.map((peer) => (
                  <button
                    key={peer.peer_id}
                    className="peer-card"
                    onClick={() => handleOpenPeer(peer.peer_id, peer.addr, peer.name || peer.peer_id)}
                  >
                    <div className={`peer-card-avatar ${peer.blocked ? "blocked" : "online"}`}>
                      {getPeerAvatar(peer.peer_id, peer.name || peer.peer_id)}
                    </div>
                    <div className="peer-card-info">
                      <div className="peer-card-info-top">
                        <strong>{peer.name || peer.peer_id}</strong>
                      </div>
                      <div className="peer-card-info-bottom">
                        <span>{peer.addr}</span>
                        <span className={`live-tag ${peer.blocked ? "blocked" : ""}`}>
                          {peer.blocked ? "blocked" : "live"}
                        </span>
                      </div>
                    </div>
                  </button>
                ))}
                {!filteredNearbyPeers.length ? (
                  <div className="empty-state compact">No nearby peers.</div>
                ) : null}
              </>
            ) : null}

            {sidebarView === "contacts" ? (
              <>
                {filteredContacts.map((contact) => (
                  <div key={contact.peer_id} className="chat-list-row">
                    <button
                      className="peer-card"
                      onContextMenu={(event) => handleContactContextMenu(event, contact.peer_id)}
                      onClick={() => {
                        setContactContextMenu(null);
                        void handleOpenPeer(
                          contact.peer_id,
                          joinAddress(contact.ip, contact.port),
                          contact.name || contact.peer_id,
                        );
                      }}
                    >
                      <div className="peer-card-avatar">
                        {getPeerAvatar(contact.peer_id, contact.name || contact.peer_id)}
                      </div>
                      <div className="peer-card-info">
                        <div className="peer-card-info-top">
                          <strong>{contact.name || contact.peer_id}</strong>
                        </div>
                        <div className="peer-card-info-bottom">
                          <span>{contact.ip}:{contact.port}</span>
                          <span className="live-tag">saved</span>
                        </div>
                      </div>
                    </button>
                  </div>
                ))}
                {!filteredContacts.length ? (
                  <div className="empty-state compact">No contacts yet.</div>
                ) : null}
              </>
            ) : null}

            {sidebarView === "blocked" ? (
              <>
                <div className="sidebar-blocked-list">
                  {filteredBlockedPeers.map((item) => (
                    <div key={item.peer_id} className="blocked-item sidebar-blocked-item">
                      <div className="blocked-info">
                        <strong>{item.name || "Unknown"}</strong>
                        <span>{item.reason || item.peer_id}</span>
                      </div>
                      <button className="ghost-tiny" onClick={() => void handleUnblock(item.peer_id)}>
                        Unblock
                      </button>
                    </div>
                  ))}
                </div>
                {!filteredBlockedPeers.length ? (
                  <div className="empty-state compact">Black list is empty.</div>
                ) : null}
              </>
            ) : null}
          </div>

          {sidebarView === "chats" || sidebarView === "contacts" ? (
            <div className="peers-panel-footer">
              <button type="button" className="new-contact-button" onClick={openManualContactPopover}>
                + New contact
              </button>
            </div>
          ) : null}
        </aside>

        {/* ─── Main Chat Area ─── */}
        <div className="main-chat main-chat-layout">
          <div style={{ flex: 1, display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
            {/* Chat Header */}
            <div className="chat-header">
              <div className="chat-header-left">
                <div className="chat-header-title">
                  {selectedChat ? (
                    <button
                      type="button"
                      className="chat-header-name"
                      onClick={() => setShowDetailPanel((value) => !value)}
                    >
                      {selectedChat.title}
                    </button>
                  ) : (
                    <h2>Select a chat</h2>
                  )}
                  {selectedChat ? (
                    <div
                      className={
                        selectedChat.blocked
                          ? "blocked-dot"
                          : selectedChat.online
                            ? "online-dot"
                            : "offline-dot"
                      }
                    />
                  ) : null}
                </div>
              </div>
              <div className="chat-header-right">
                {selectedChat ? (
                  <>
                    {!selectedContact && (
                      <button className="header-btn" onClick={prefillFromCurrentPeer}>
                        + Add Contact
                      </button>
                    )}
                    <button
                      className="kebab-btn"
                      onClick={() => setShowDetailPanel((v) => !v)}
                      title="Toggle peer details"
                    >
                      ⋮
                    </button>
                  </>
                ) : null}
              </div>
            </div>

            {/* Messages */}
            <section style={{ flex: 1, display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
              {loading ? (
                <div className="empty-state">
                  <div>
                    <h3>Connecting to backend</h3>
                    <p>The Tauri shell expects the Go bridge at {getApiBase()}.</p>
                  </div>
                </div>
              ) : selectedChat ? (
                <>
                  <div ref={messageStreamRef} className="message-stream">
                    {/* Session separator */}
                    {firstMsgDate && (
                      <div className="session-separator">
                        <span>{firstMsgDate}</span>
                      </div>
                    )}

                    {selectedMessages.map((message) => (
                      <div
                        key={message.message_id ?? `${message.timestamp}-${message.from}`}
                        className={`msg-row ${message.direction}`}
                      >
                        <div className={`msg-content ${message.direction}`}>
                          <div className={`msg-bubble ${message.direction}`}>
                            <p className="msg-text">{message.text}</p>
                            <div className="msg-meta">
                              <span className="msg-time">{formatTime(message.timestamp)}</span>
                            </div>
                          </div>
                        </div>
                      </div>
                    ))}

                    {!selectedMessages.length ? (
                      <div className="empty-state">
                        <div>
                          <h3>Chat is open</h3>
                          <p>Handshake is ready. Send the first message.</p>
                        </div>
                      </div>
                    ) : null}
                  </div>

                  {/* Composer */}
                  <div className="composer">
                    <div className="composer-input-row">
                      <textarea
                        placeholder="Type a message or drop a file..."
                        value={composer}
                        onChange={(e) => setComposer(e.target.value)}
                        onKeyDown={(e) => {
                          if (e.key === "Enter" && !e.shiftKey) {
                            e.preventDefault();
                            void handleSend();
                          }
                        }}
                      />
                      <button className="send-btn" onClick={() => void handleSend()} title="Send">
                        ➤
                      </button>
                    </div>
                  </div>
                </>
              ) : (
                <div className="empty-state">
                  <div>
                    <h3>No active conversation</h3>
                    <p>Pick a chat or open a nearby peer from the panel.</p>
                  </div>
                </div>
              )}
            </section>
          </div>

          {/* ─── Detail Panel (right side, togglable) ─── */}
          {selectedChat && showDetailPanel ? (
            <aside className="detail-panel">
              <div className="detail-banner" />

              <div className="detail-hero">
                <div className="emoji-anchor">
                  <button
                    type="button"
                    className="detail-avatar"
                    onClick={() => setEmojiPickerTarget((c) => (c === "peer" ? null : "peer"))}
                  >
                    {selectedPeerEmoji}
                    <div
                      className={`status-indicator ${selectedChat.blocked ? "blocked" : selectedChat.online ? "online" : ""
                        }`}
                    />
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

                <div className="detail-name-block">
                  {editingPeerName ? (
                    <input
                      className="inline-name-input"
                      value={peerNameDraft}
                      autoFocus
                      onChange={(e) => setPeerNameDraft(e.target.value)}
                      onBlur={() => void handleCommitPeerName()}
                      onKeyDown={(e) => {
                        if (e.key === "Enter") {
                          e.preventDefault();
                          void handleCommitPeerName();
                        }
                        if (e.key === "Escape") {
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

              <div className="detail-content">
                <div className="detail-divider" />

                <div className="info-section">
                  <span className="section-label">PEER ID</span>
                  <div className="id-copy-box" onClick={() => navigator.clipboard.writeText(selectedChat.peer_id)}>
                    <code>{selectedChat.peer_id}</code>
                  </div>
                </div>

                {selectedAddr && (
                  <div className="info-section">
                    <span className="section-label">ADDRESS</span>
                    <div className="id-copy-box" onClick={() => navigator.clipboard.writeText(selectedAddr)}>
                      <code>{selectedAddr}</code>
                    </div>
                  </div>
                )}
              </div>

              <div className="detail-footer">
                <div className="inline-block-form">
                  <input
                    placeholder="Reason..."
                    value={blockReason}
                    onChange={(e) => setBlockReason(e.target.value)}
                  />
                  <button className="danger-btn" onClick={() => void handleBlock()}>
                    Block
                  </button>
                </div>
              </div>
            </aside>
          ) : null}
        </div>
      </div>
      {/* ─── Settings Popover (modal) ─── */}
      {showSettings ? (
        <>
          <div className="contact-popover-backdrop" onClick={closeSettings} />
          <div className="settings-popover" role="dialog" aria-modal="true" aria-labelledby="settings-title">
            <div className="settings-popover-head">
              <div className="settings-popover-title">
                {activeSettingsItem ? (
                  <button
                    type="button"
                    className="ghost-tiny"
                    onClick={() => setActiveSettingsSection(null)}
                  >
                    Back
                  </button>
                ) : (
                  <span className="settings-popover-kicker">Preferences</span>
                )}
                <h2 id="settings-title">{activeSettingsItem?.label ?? "Settings"}</h2>
              </div>
              <button type="button" className="ghost-tiny" onClick={closeSettings}>
                Close
              </button>
            </div>

            {activeSettingsItem ? (
              activeSettingsSection === "appearance" ? (
                <div className="appearance-panel">
                  <section className="appearance-section">
                    <div className="appearance-section-head">
                      <span className="appearance-section-label">Theme</span>
                      <span className="appearance-section-meta">{resolvedTheme}</span>
                    </div>
                    <div className="appearance-theme-grid">
                      {THEME_OPTIONS.map((theme) => (
                        <button
                          key={theme.id}
                          type="button"
                          className={`appearance-choice ${themePreference === theme.id ? "active" : ""}`}
                          onClick={() => setThemePreference(theme.id)}
                        >
                          {theme.label}
                        </button>
                      ))}
                    </div>
                  </section>

                  <section className="appearance-section">
                    <div className="appearance-section-head">
                      <span className="appearance-section-label">Icon</span>
                      <span className="appearance-section-meta">{currentAppIcon.label}</span>
                    </div>
                    <div className="appearance-icon-current">
                      <img src={currentAppIcon.src} alt={currentAppIcon.label} className="appearance-icon-current-image" />
                      <div className="appearance-icon-current-copy">
                        <strong>{currentAppIcon.label}</strong>
                        <span>Saved locally for this device</span>
                      </div>
                    </div>
                    <div className="appearance-icon-grid">
                      {APP_ICON_OPTIONS.map((icon) => (
                        <button
                          key={icon.id}
                          type="button"
                          className={`appearance-icon-card ${selectedAppIcon === icon.id ? "active" : ""}`}
                          onClick={() => setSelectedAppIcon(icon.id)}
                        >
                          <img src={icon.src} alt={icon.label} className="appearance-icon-image" />
                          <span>{icon.label}</span>
                        </button>
                      ))}
                    </div>
                  </section>
                </div>
              ) : (
                <div className="settings-panel-empty" />
              )
            ) : (
              <div className="settings-menu">
                {SETTINGS_SECTIONS.map((section) => (
                  <button
                    key={section.id}
                    type="button"
                    className="settings-menu-item"
                    onClick={() => setActiveSettingsSection(section.id)}
                  >
                    <span>{section.label}</span>
                    <span className="settings-menu-arrow" aria-hidden="true">›</span>
                  </button>
                ))}
              </div>
            )}
          </div>
        </>
      ) : null}
      {/* ─── Contact Popover (modal) ─── */}
      {showNewContactPopover ? (
        <>
          <div className="contact-popover-backdrop" onClick={() => setShowNewContactPopover(false)} />
          <div className="contact-popover">
            <div className="popover-head">
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
                  onChange={(e) => setInvitePeerIdDraft(e.target.value.replace(/\D/g, "").slice(0, 6))}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") {
                      e.preventDefault();
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
                  onChange={(e) => setContactForm((c) => ({ ...c, name: e.target.value }))}
                />
              </label>
              <label>
                <span>IP</span>
                <input
                  value={contactForm.ip}
                  onChange={(e) => setContactForm((c) => ({ ...c, ip: e.target.value }))}
                />
              </label>
              <label>
                <span>Peer ID</span>
                <input
                  value={contactForm.peer_id}
                  onChange={(e) => setContactForm((c) => ({ ...c, peer_id: e.target.value }))}
                />
              </label>
              <label>
                <span>Port</span>
                <input
                  value={contactForm.port}
                  onChange={(e) => setContactForm((c) => ({ ...c, port: e.target.value }))}
                />
              </label>
            </div>
            <div className="action-row">
              <button className="ghost" disabled={!invitePeerIdDraft || saving} onClick={() => void handleOpenInvitePeer()}>
                Open by code
              </button>
              <button className="ghost" disabled={!selectedChat} onClick={prefillFromCurrentPeer}>
                Use current peer
              </button>
              <button className="primary" disabled={saving} onClick={() => void handleSaveContact()}>
                Save contact
              </button>
            </div>
          </div>
        </>
      ) : null}

      {/* ─── Error Toast ─── */}
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

      {chatContextMenu ? (
        <div
          className="chat-context-menu"
          style={{
            top: Math.max(16, chatContextMenu.y),
            left: Math.max(16, chatContextMenu.x),
          }}
          onPointerDown={(event) => event.stopPropagation()}
        >
          <button
            type="button"
            className="chat-context-menu-item danger"
            onClick={() => handleHideChat(chatContextMenu.chatId)}
          >
            Удалить чат 🗑️
          </button>
        </div>
      ) : null}

      {contactContextMenu ? (
        <div
          className="chat-context-menu"
          style={{
            top: Math.max(16, contactContextMenu.y),
            left: Math.max(16, contactContextMenu.x),
          }}
          onPointerDown={(event) => event.stopPropagation()}
        >
          <button
            type="button"
            className="chat-context-menu-item danger"
            onClick={() => void handleDeleteContact(contactContextMenu.peerId)}
          >
            Удалить контакт 🗑️
          </button>
        </div>
      ) : null}
    </>
  );
}
