const PROVIDER_BACKGROUND_CACHE_KEY = "orvion_provider_background_cache";
const PROVIDER_BACKGROUND_DB_NAME = "orvion_local_assets";
const PROVIDER_BACKGROUND_STORE_NAME = "images";
const PROVIDER_BACKGROUND_CACHE_VERSION = 1;

interface ProviderBackgroundMetadata {
  version: number;
  source: string;
  updatedAt: number;
  size: number;
  type: string;
}

interface ProviderBackgroundRecord extends ProviderBackgroundMetadata {
  blob: Blob;
}

function readMetadata(source: string): ProviderBackgroundMetadata | null {
  if (typeof window === "undefined") return null;

  try {
    const raw = window.localStorage.getItem(PROVIDER_BACKGROUND_CACHE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Partial<ProviderBackgroundMetadata>;
    if (
      parsed.version !== PROVIDER_BACKGROUND_CACHE_VERSION
      || parsed.source !== source
      || typeof parsed.updatedAt !== "number"
      || typeof parsed.size !== "number"
      || typeof parsed.type !== "string"
    ) {
      return null;
    }
    return parsed as ProviderBackgroundMetadata;
  } catch {
    return null;
  }
}

function saveMetadata(metadata: ProviderBackgroundMetadata): void {
  if (typeof window === "undefined") return;

  try {
    window.localStorage.setItem(PROVIDER_BACKGROUND_CACHE_KEY, JSON.stringify(metadata));
  } catch {
    // localStorage 不可用时仍保留 IndexedDB 原图缓存，不影响页面显示。
  }
}

function openDatabase(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    if (typeof indexedDB === "undefined") {
      reject(new Error("IndexedDB unavailable"));
      return;
    }

    const request = indexedDB.open(PROVIDER_BACKGROUND_DB_NAME, 1);
    request.onupgradeneeded = () => {
      const database = request.result;
      if (!database.objectStoreNames.contains(PROVIDER_BACKGROUND_STORE_NAME)) {
        database.createObjectStore(PROVIDER_BACKGROUND_STORE_NAME, { keyPath: "source" });
      }
    };
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error ?? new Error("Failed to open IndexedDB"));
  });
}

async function readCachedImage(source: string): Promise<Blob | null> {
  const metadata = readMetadata(source);
  if (!metadata) return null;

  const database = await openDatabase();
  return new Promise((resolve, reject) => {
    const transaction = database.transaction(PROVIDER_BACKGROUND_STORE_NAME, "readonly");
    const request = transaction.objectStore(PROVIDER_BACKGROUND_STORE_NAME).get(source);
    request.onsuccess = () => {
      const record = request.result as ProviderBackgroundRecord | undefined;
      resolve(record?.blob instanceof Blob ? record.blob : null);
    };
    request.onerror = () => reject(request.error ?? new Error("Failed to read cached image"));
    transaction.oncomplete = () => database.close();
    transaction.onerror = () => reject(transaction.error ?? new Error("Failed to read cached image"));
  });
}

async function saveCachedImage(source: string, blob: Blob): Promise<void> {
  const updatedAt = Date.now();
  const metadata: ProviderBackgroundMetadata = {
    version: PROVIDER_BACKGROUND_CACHE_VERSION,
    source,
    updatedAt,
    size: blob.size,
    type: blob.type || "image/*",
  };

  const database = await openDatabase();
  await new Promise<void>((resolve, reject) => {
    const transaction = database.transaction(PROVIDER_BACKGROUND_STORE_NAME, "readwrite");
    transaction.objectStore(PROVIDER_BACKGROUND_STORE_NAME).put({ ...metadata, blob } satisfies ProviderBackgroundRecord);
    transaction.oncomplete = () => resolve();
    transaction.onerror = () => reject(transaction.error ?? new Error("Failed to write cached image"));
    transaction.onabort = () => reject(transaction.error ?? new Error("Failed to write cached image"));
  });
  database.close();
  saveMetadata(metadata);
}

export interface ProviderBackgroundCacheController {
  refresh: () => void;
  dispose: () => void;
}

/**
 * 创建背景图缓存控制器。
 * 原图二进制保存在 IndexedDB，localStorage 仅保存版本、大小和更新时间等索引信息。
 */
export function createProviderBackgroundCacheController(
  source: string,
  onUpdated: (src: string) => void,
): ProviderBackgroundCacheController {
  if (typeof window === "undefined" || typeof URL === "undefined") {
    return { refresh: () => undefined, dispose: () => undefined };
  }

  let active = true;
  let activeObjectURL: string | null = null;
  let refreshInFlight: Promise<void> | null = null;

  const publish = (blob: Blob) => {
    if (!active) return;
    const nextObjectURL = URL.createObjectURL(blob);
    if (activeObjectURL) {
      URL.revokeObjectURL(activeObjectURL);
    }
    activeObjectURL = nextObjectURL;
    onUpdated(nextObjectURL);
  };

  const loadAndRefresh = async () => {
    try {
      const cachedBlob = await readCachedImage(source);
      if (cachedBlob) {
        publish(cachedBlob);
      }
    } catch {
      // 本地缓存不可读时直接走网络刷新。
    }

    try {
      const response = await fetch(source, { cache: "no-cache" });
      if (!response.ok) return;
      const blob = await response.blob();
      if (!blob.type.startsWith("image/")) return;
      await saveCachedImage(source, blob);
      publish(blob);
    } catch {
      // 网络刷新失败时继续使用已读取的本地原图或默认资源 URL。
    }
  };

  const refresh = () => {
    if (!active || refreshInFlight) return;
    refreshInFlight = loadAndRefresh().finally(() => {
      refreshInFlight = null;
    });
  };

  const dispose = () => {
    active = false;
    if (activeObjectURL) {
      URL.revokeObjectURL(activeObjectURL);
      activeObjectURL = null;
    }
  };

  refresh();
  return { refresh, dispose };
}
