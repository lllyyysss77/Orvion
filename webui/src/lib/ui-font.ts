export const UI_FONT_STORAGE_KEY = "orvion_ui_font";
export const UI_FONT_CHANGED_EVENT = "ui-font-changed";

export type UIFontOption = "default" | "kunming_seagull" | "fenyuan" | "lxgw_wenkai";

interface UIFontDefinition {
  family: string;
  loadURL: () => Promise<string>;
}

const fontDefinitions: Record<Exclude<UIFontOption, "default">, UIFontDefinition> = {
  kunming_seagull: {
    family: "KunmingSeagull",
    loadURL: () => import("@/assets/fonts/kunming-seagull.woff2?url").then((module) => module.default),
  },
  fenyuan: {
    family: "JustFontFenyuan",
    loadURL: () => import("@/assets/fonts/justfont-fenyuan.woff2?url").then((module) => module.default),
  },
  lxgw_wenkai: {
    family: "LXGWWenKai",
    loadURL: () => import("@/assets/fonts/lxgw-wenkai.woff2?url").then((module) => module.default),
  },
};

const loadingFonts = new Map<UIFontOption, Promise<void>>();

export const isUIFontOption = (value: string | null | undefined): value is UIFontOption => (
  value === "default" || value === "kunming_seagull" || value === "fenyuan" || value === "lxgw_wenkai"
);

export async function loadUIFont(font: UIFontOption): Promise<void> {
  if (font === "default" || typeof document === "undefined") return;

  const existing = loadingFonts.get(font);
  if (existing) return existing;

  const definition = fontDefinitions[font];
  const loading = definition.loadURL().then(async (url) => {
    if (document.fonts.check(`1em "${definition.family}"`)) return;
    const face = new FontFace(definition.family, `url("${url}") format("woff2")`, {
      style: "normal",
      weight: "400",
      display: "swap",
    });
    await face.load();
    document.fonts.add(face);
  }).catch((error) => {
    loadingFonts.delete(font);
    throw error;
  });

  loadingFonts.set(font, loading);
  return loading;
}

export async function applyUIFont(font: UIFontOption): Promise<void> {
  if (typeof window !== "undefined") {
    window.localStorage.setItem(UI_FONT_STORAGE_KEY, font);
  }
  await loadUIFont(font);
  if (typeof document !== "undefined") {
    document.documentElement.dataset.uiFont = font;
  }
}

export function notifyUIFontChanged(font: UIFontOption): void {
  if (typeof window === "undefined") return;
  window.localStorage.setItem(UI_FONT_STORAGE_KEY, font);
  window.dispatchEvent(new CustomEvent(UI_FONT_CHANGED_EVENT, { detail: { font } }));
}
