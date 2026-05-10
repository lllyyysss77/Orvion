import { useEffect, useState } from 'react';
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
    if (style === "orbit_ring") {
      return (
        <div className="flex h-8 items-center justify-center">
          <div className="loader-min-orbit relative size-8 rounded-full border border-border/70">
            <span className="absolute left-1/2 top-0 size-2 -translate-x-1/2 rounded-full bg-foreground/75" />
          </div>
        </div>
      );
    }

    if (style === "slim_progress") {
      return (
        <div className="flex h-8 items-center px-2">
          <div className="relative h-1.5 w-full overflow-hidden rounded-full bg-muted-foreground/20">
            <span className="loader-min-sweep absolute inset-y-0 left-0 w-2/5 rounded-full bg-foreground/70" />
          </div>
        </div>
      );
    }

    if (style === "ripple_focus") {
      return (
        <div className="flex h-8 items-center justify-center">
          <div className="relative size-9">
            <span className="loader-min-ripple-1 absolute inset-0 rounded-full border border-foreground/50" />
            <span className="loader-min-ripple-2 absolute inset-0 rounded-full border border-foreground/35" />
            <span className="absolute left-1/2 top-1/2 size-1.5 -translate-x-1/2 -translate-y-1/2 rounded-full bg-foreground/75" />
          </div>
        </div>
      );
    }

    return (
      <div className="flex h-8 items-end justify-center gap-1.5">
        <span className="loader-min-line-1 h-4 w-1.5 rounded-full bg-foreground/70" />
        <span className="loader-min-line-2 h-6 w-1.5 rounded-full bg-foreground/60" />
        <span className="loader-min-line-3 h-4 w-1.5 rounded-full bg-foreground/50" />
        <span className="loader-min-line-4 h-6 w-1.5 rounded-full bg-foreground/60" />
        <span className="loader-min-line-5 h-4 w-1.5 rounded-full bg-foreground/70" />
      </div>
    );
  };

  return (
    <div className={`flex flex-col items-center justify-center gap-3 ${className}`}>
      <div
        className="relative w-[196px] rounded-xl border border-border/60 bg-card/85 p-2.5 shadow-sm"
        role="status"
        aria-live="polite"
        aria-label={normalizedMessage}
      >
        <div className="rounded-lg bg-background/70 px-2">
          {renderVisual()}
        </div>
      </div>
      <div className="text-sm font-medium text-muted-foreground">{normalizedMessage}</div>
    </div>
  );
};

export default Loading;
