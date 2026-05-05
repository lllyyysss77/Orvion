export const LOADING_UI_STORAGE_KEY = "orvion_ui_loading_style";

export const loadingUIValues = ["rocket_trail", "star_dash", "jelly_wave", "candy_slide"] as const;

export type LoadingUIStyle = (typeof loadingUIValues)[number];

export const resolveLoadingUIStyle = (style?: string): LoadingUIStyle => {
  if (!style) return "rocket_trail";
  if (loadingUIValues.includes(style as LoadingUIStyle)) {
    return style as LoadingUIStyle;
  }
  return "rocket_trail";
};

export const readStoredLoadingUIStyle = (): LoadingUIStyle => {
  if (typeof window === "undefined") return "rocket_trail";
  return resolveLoadingUIStyle(window.localStorage.getItem(LOADING_UI_STORAGE_KEY) ?? undefined);
};

export const applyLoadingUIStyleSetting = (style: LoadingUIStyle) => {
  if (typeof window !== "undefined") {
    window.localStorage.setItem(LOADING_UI_STORAGE_KEY, style);
    window.dispatchEvent(new CustomEvent("ui-loading-style-changed", { detail: { style } }));
  }
  if (typeof document !== "undefined") {
    document.documentElement.dataset.uiLoadingStyle = style;
  }
};
