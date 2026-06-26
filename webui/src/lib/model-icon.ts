type ModelIconAsset = {
  key: string;
  normalizedKey: string;
  tokens: string[];
  src: string;
  alt: string;
  formatPriority: number;
};

const iconModules = import.meta.glob("../assets/modelIcon/*.{svg,png,jpg,jpeg,webp,avif,gif,ico}", {
  eager: true,
  import: "default",
}) as Record<string, string>;

const normalizeForMatch = (value: string): string =>
  value
    .toLowerCase()
    .normalize("NFKC")
    .replace(/[^a-z0-9]/g, "");

const tokenizeForMatch = (value: string): string[] =>
  value
    .toLowerCase()
    .normalize("NFKC")
    .split(/[^a-z0-9]+/g)
    .map((item) => item.trim())
    .filter((item) => item.length >= 2);

const toTitle = (value: string): string =>
  value
    .split(/[_\-\s]+/g)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1).toLowerCase())
    .join(" ");

const getIconFormatPriority = (filename: string): number => {
  const extension = filename.split(".").pop()?.toLowerCase() ?? "";
  switch (extension) {
    case "svg":
      return 5;
    case "png":
    case "webp":
    case "avif":
      return 4;
    case "jpg":
    case "jpeg":
      return 3;
    case "gif":
      return 2;
    case "ico":
      return 1;
    default:
      return 0;
  }
};

const modelIconAssets: ModelIconAsset[] = Object.entries(iconModules)
  .map(([path, src]) => {
    const filename = path.split("/").pop() ?? "";
    const key = filename.replace(/\.[^.]+$/, "");
    return {
      key,
      normalizedKey: normalizeForMatch(key),
      tokens: tokenizeForMatch(key),
      src,
      alt: toTitle(key),
      formatPriority: getIconFormatPriority(filename),
    };
  })
  .filter((item) => item.normalizedKey.length >= 2)
  .sort((a, b) => b.normalizedKey.length - a.normalizedKey.length || b.formatPriority - a.formatPriority);

const modelIconAliases = [
  {
    targetKey: "qwen",
    patterns: ["z-image", "z-image-turbo", "z-iamge", "z-iamge-turbo"],
  },
].map((item) => ({
  targetKey: normalizeForMatch(item.targetKey),
  patterns: item.patterns.map(normalizeForMatch).filter(Boolean),
}));

const resolveAliasedAsset = (modelNormalized: string, modelTokens: string[]): ModelIconAsset | null => {
  for (const alias of modelIconAliases) {
    const matched = alias.patterns.some(
      (pattern) =>
        modelNormalized === pattern ||
        modelNormalized.startsWith(pattern) ||
        modelTokens.includes(pattern)
    );
    if (!matched) continue;
    return modelIconAssets.find((asset) => asset.normalizedKey === alias.targetKey) ?? null;
  }
  return null;
};

const getMatchScore = (modelNormalized: string, modelTokens: string[], asset: ModelIconAsset): number => {
  const key = asset.normalizedKey;
  if (!key) return -1;
  if (modelNormalized === key) return 10000 + key.length;
  if (modelTokens.includes(key)) return 9000 + key.length;
  if (modelNormalized.startsWith(key)) return 8000 + key.length;
  if (modelNormalized.includes(key)) return 7000 + key.length;
  if (key.includes(modelNormalized) && modelNormalized.length >= 3) return 6000 + modelNormalized.length;

  for (const token of modelTokens) {
    if (token === key) return 5000 + key.length;
    if (token.startsWith(key)) return 4000 + key.length;
    if (token.includes(key)) return 3000 + key.length;
    if (key.includes(token) && token.length >= 3) return 2000 + token.length;
  }
  return -1;
};

export type ResolvedModelIcon = {
  src: string;
  alt: string;
  key: string;
};

export const resolveModelIcon = (modelName: string): ResolvedModelIcon | null => {
  const raw = (modelName ?? "").trim();
  if (!raw) return null;

  const modelNormalized = normalizeForMatch(raw);
  if (!modelNormalized) return null;
  const modelTokens = tokenizeForMatch(raw);
  const aliasedAsset = resolveAliasedAsset(modelNormalized, modelTokens);
  if (aliasedAsset) {
    return {
      src: aliasedAsset.src,
      alt: aliasedAsset.alt || aliasedAsset.key,
      key: aliasedAsset.key,
    };
  }

  let best: ModelIconAsset | null = null;
  let bestScore = -1;
  for (const asset of modelIconAssets) {
    const score = getMatchScore(modelNormalized, modelTokens, asset);
    if (score > bestScore) {
      bestScore = score;
      best = asset;
    }
  }
  if (!best || bestScore < 0) return null;

  return {
    src: best.src,
    alt: best.alt || best.key,
    key: best.key,
  };
};
