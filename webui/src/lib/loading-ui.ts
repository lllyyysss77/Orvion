export const LOADING_UI_STORAGE_KEY = "orvion_ui_loading_style";

export const loadingUIValues = ["line_pulse", "orbit_ring", "slim_progress", "ripple_focus"] as const;

export type LoadingUIStyle = (typeof loadingUIValues)[number];

export const resolveLoadingUIStyle = (style?: string): LoadingUIStyle => {
  if (!style) return "line_pulse";
  if (loadingUIValues.includes(style as LoadingUIStyle)) {
    return style as LoadingUIStyle;
  }
  return "line_pulse";
};

export const readStoredLoadingUIStyle = (): LoadingUIStyle => {
  if (typeof window === "undefined") return "line_pulse";
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
