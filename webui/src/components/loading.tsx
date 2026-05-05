import { useEffect, useState } from 'react';
import { Heart, Rocket, Sparkles, Star } from "lucide-react";
import {
  readStoredLoadingUIStyle,
  resolveLoadingUIStyle,
  type LoadingUIStyle,
  LOADING_UI_STORAGE_KEY,
} from "@/lib/loading-ui";

interface LoadingProps {
  message?: string;
  className?: string;
}

const Loading: React.FC<LoadingProps> = ({ message = '加载中', className = '' }) => {
  const [style, setStyle] = useState<LoadingUIStyle>(() => readStoredLoadingUIStyle());
  const normalizedMessage = message.replace(/\.{3,}$/g, '');

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }

    const handleStyleChanged = (event: Event) => {
      const customEvent = event as CustomEvent<{ style?: string }>;
      setStyle(resolveLoadingUIStyle(customEvent.detail?.style));
    };

    const handleStorageChanged = (event: StorageEvent) => {
      if (event.key !== LOADING_UI_STORAGE_KEY) {
        return;
      }
      setStyle(resolveLoadingUIStyle(event.newValue ?? undefined));
    };

    window.addEventListener("ui-loading-style-changed", handleStyleChanged as EventListener);
    window.addEventListener("storage", handleStorageChanged);
    return () => {
      window.removeEventListener("ui-loading-style-changed", handleStyleChanged as EventListener);
      window.removeEventListener("storage", handleStorageChanged);
    };
  }, []);

  const renderVisual = () => {
    if (style === "star_dash") {
      return (
        <div className="relative h-7 overflow-hidden rounded-xl bg-background/70">
          <div className="absolute inset-x-3 top-1/2 h-1.5 -translate-y-1/2 rounded-full border border-dashed border-muted-foreground/30" />
          <div className="loader-cute-star absolute left-2 top-[3px] inline-flex size-6 items-center justify-center rounded-full bg-white/90 text-fuchsia-500 shadow-sm dark:bg-zinc-900/90">
            <Star className="size-3.5" />
          </div>
          <Sparkles className="loader-cute-sparkle-left absolute right-9 top-[4px] size-3 text-amber-500" />
          <Sparkles className="loader-cute-sparkle-right absolute right-4 top-[11px] size-2.5 text-sky-500" />
        </div>
      );
    }

    if (style === "jelly_wave") {
      return (
        <div className="relative h-7 rounded-xl bg-background/70 px-3">
          <div className="flex h-full items-center gap-2">
            <span className="loader-cute-jelly-1 h-3.5 w-10 rounded-full bg-gradient-to-r from-rose-300 to-amber-300 dark:from-rose-700 dark:to-amber-700" />
            <span className="loader-cute-jelly-2 h-3.5 w-10 rounded-full bg-gradient-to-r from-sky-300 to-cyan-300 dark:from-sky-700 dark:to-cyan-700" />
            <span className="loader-cute-jelly-3 h-3.5 w-10 rounded-full bg-gradient-to-r from-emerald-300 to-lime-300 dark:from-emerald-700 dark:to-lime-700" />
          </div>
        </div>
      );
    }

    if (style === "candy_slide") {
      return (
        <div className="relative h-7 overflow-hidden rounded-xl bg-background/70">
          <div className="loader-cute-candy absolute inset-y-[5px] left-0 w-14 rounded-full bg-gradient-to-r from-pink-300 via-violet-300 to-indigo-300 dark:from-pink-700 dark:via-violet-700 dark:to-indigo-700" />
          <div className="absolute inset-x-3 top-1/2 h-1.5 -translate-y-1/2 rounded-full bg-muted-foreground/15" />
          <div className="loader-cute-heart absolute left-2 top-[3px] inline-flex size-6 items-center justify-center rounded-full bg-white/90 text-rose-500 shadow-sm dark:bg-zinc-900/90">
            <Heart className="size-3.5" />
          </div>
        </div>
      );
    }

    return (
      <div className="relative h-7 overflow-hidden rounded-xl bg-background/70">
        <div className="absolute inset-x-3 top-1/2 h-1.5 -translate-y-1/2 rounded-full bg-muted-foreground/15" />
        <div className="loader-cute-rocket absolute left-2 top-[3px] inline-flex size-6 items-center justify-center rounded-full bg-white/90 text-rose-500 shadow-sm dark:bg-zinc-900/90">
          <Rocket className="size-3.5" />
        </div>
        <Sparkles className="loader-cute-sparkle-left absolute right-9 top-[4px] size-3 text-amber-500" />
        <Sparkles className="loader-cute-sparkle-right absolute right-4 top-[11px] size-2.5 text-sky-500" />
      </div>
    );
  };

  return (
    <div className={`flex flex-col items-center justify-center gap-3 ${className}`}>
      <div
        className="relative w-[196px] rounded-2xl border border-border/60 bg-gradient-to-r from-emerald-100/80 via-amber-100/80 to-sky-100/80 p-2 shadow-sm dark:from-emerald-950/40 dark:via-amber-950/40 dark:to-sky-950/40"
        role="status"
        aria-live="polite"
        aria-label={normalizedMessage}
      >
        {renderVisual()}
      </div>
      <div className="text-sm font-medium text-muted-foreground">{normalizedMessage}</div>
    </div>
  );
};

export default Loading;
