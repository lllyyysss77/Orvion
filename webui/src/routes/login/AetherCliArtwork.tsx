import { useEffect, useId, useMemo, useRef, useState, type CSSProperties } from "react";
import type { CliSectionItem } from "./home-config";

type AetherCliArtworkProps = {
  id: CliSectionItem["id"];
  darkMode: boolean;
  replayToken: number;
};

const claudePath =
  "M4.709 15.955l4.72-2.647.08-.23-.08-.128H9.2l-.79-.048-2.698-.073-2.339-.097-2.266-.122-.571-.121L0 11.784l.055-.352.48-.321.686.06 1.52.103 2.278.158 1.652.097 2.449.255h.389l.055-.157-.134-.098-.103-.097-2.358-1.596-2.552-1.688-1.336-.972-.724-.491-.364-.462-.158-1.008.656-.722.881.06.225.061.893.686 1.908 1.476 2.491 1.833.365.304.145-.103.019-.073-.164-.274-1.355-2.446-1.446-2.49-.644-1.032-.17-.619a2.97 2.97 0 01-.104-.729L6.283.134 6.696 0l.996.134.42.364.62 1.414 1.002 2.229 1.555 3.03.456.898.243.832.091.255h.158V9.01l.128-1.706.237-2.095.23-2.695.08-.76.376-.91.747-.492.584.28.48.685-.067.444-.286 1.851-.559 2.903-.364 1.942h.212l.243-.242.985-1.306 1.652-2.064.73-.82.85-.904.547-.431h1.033l.76 1.129-.34 1.166-1.064 1.347-.881 1.142-1.264 1.7-.79 1.36.073.11.188-.02 2.856-.606 1.543-.28 1.841-.315.833.388.091.395-.328.807-1.969.486-2.309.462-3.439.813-.042.03.049.061 1.549.146.662.036h1.622l3.02.225.79.522.474.638-.079.485-1.215.62-1.64-.389-3.829-.91-1.312-.329h-.182v.11l1.093 1.068 2.006 1.81 2.509 2.33.127.578-.322.455-.34-.049-2.205-1.657-.851-.747-1.926-1.62h-.128v.17l.444.649 2.345 3.521.122 1.08-.17.353-.608.213-.668-.122-1.374-1.925-1.415-2.167-1.143-1.943-.14.08-.674 7.254-.316.37-.729.28-.607-.461-.322-.747.322-1.476.389-1.924.315-1.53.286-1.9.17-.632-.012-.042-.14.018-1.434 1.967-2.18 2.945-1.726 1.845-.414.164-.717-.37.067-.662.401-.589 2.388-3.036 1.44-1.882.93-1.086-.006-.158h-.055L4.132 18.56l-1.13.146-.487-.456.061-.746.231-.243 1.908-1.312-.006.006z";

const openaiPath =
  "M22.2819 9.8211a5.9847 5.9847 0 0 0-.5157-4.9108 6.0462 6.0462 0 0 0-6.5098-2.9A6.0651 6.0651 0 0 0 4.9807 4.1818a5.9847 5.9847 0 0 0-3.9977 2.9 6.0462 6.0462 0 0 0 .7427 7.0966 5.98 5.98 0 0 0 .511 4.9107 6.051 6.051 0 0 0 6.5146 2.9001A5.9847 5.9847 0 0 0 13.2599 24a6.0557 6.0557 0 0 0 5.7718-4.2058 5.9894 5.9894 0 0 0 3.9977-2.9001 6.0557 6.0557 0 0 0-.7475-7.0729zm-9.022 12.6081a4.4755 4.4755 0 0 1-2.8764-1.0408l.1419-.0804 4.7783-2.7582a.7948.7948 0 0 0 .3927-.6813v-6.7369l2.02 1.1686a.071.071 0 0 1 .038.052v5.5826a4.504 4.504 0 0 1-4.4945 4.4944zm-9.6607-4.1254a4.4708 4.4708 0 0 1-.5346-3.0137l.142.0852 4.783 2.7582a.7712.7712 0 0 0 .7806 0l5.8428-3.3685v2.3324a.0804.0804 0 0 1-.0332.0615L9.74 19.9502a4.4992 4.4992 0 0 1-6.1408-1.6464zM2.3408 7.8956a4.485 4.485 0 0 1 2.3655-1.9728V11.6a.7664.7664 0 0 0 .3879.6765l5.8144 3.3543-2.0201 1.1685a.0757.0757 0 0 1-.071 0l-4.8303-2.7865A4.504 4.504 0 0 1 2.3408 7.872zm16.5963 3.8558L13.1038 8.364 15.1192 7.2a.0757.0757 0 0 1 .071 0l4.8303 2.7913a4.4944 4.4944 0 0 1-.6765 8.1042v-5.6772a.79.79 0 0 0-.407-.667zm2.0107-3.0231l-.142-.0852-4.7735-2.7818a.7759.7759 0 0 0-.7854 0L9.409 9.2297V6.8974a.0662.0662 0 0 1 .0284-.0615l4.8303-2.7866a4.4992 4.4992 0 0 1 6.6802 4.66zM8.3065 12.863l-2.02-1.1638a.0804.0804 0 0 1-.038-.0567V6.0742a4.4992 4.4992 0 0 1 7.3757-3.4537l-.142.0805L8.704 5.459a.7948.7948 0 0 0-.3927.6813zm1.0976-2.3654l2.602-1.4998 2.6069 1.4998v2.9994l-2.5974 1.4997-2.6067-1.4997Z";

const geminiPath =
  "M20.616 10.835a14.147 14.147 0 01-4.45-3.001 14.111 14.111 0 01-3.678-6.452.503.503 0 00-.975 0 14.134 14.134 0 01-3.679 6.452 14.155 14.155 0 01-4.45 3.001c-.65.28-1.318.505-2.002.678a.502.502 0 000 .975c.684.172 1.35.397 2.002.677a14.147 14.147 0 014.45 3.001 14.112 14.112 0 013.679 6.453.502.502 0 00.975 0c.172-.685.397-1.351.677-2.003a14.145 14.145 0 013.001-4.45 14.113 14.113 0 016.453-3.678.503.503 0 000-.975 13.245 13.245 0 01-2.003-.678z";
const geminiStarPath =
  "M12 1.5c.2 3.4 1.4 6.4 3.8 8.8 2.4 2.4 5.4 3.6 8.8 3.8-3.4.2-6.4 1.4-8.8 3.8-2.4 2.4-3.6 5.4-3.8 8.8-.2-3.4-1.4-6.4-3.8-8.8-2.4-2.4-5.4-3.6-8.8-3.8 3.4-.2 6.4-1.4 8.8-3.8 2.4-2.4 3.6-5.4 3.8-8.8z";

function useDrawComplete(delayMs: number, replayToken: number) {
  const [done, setDone] = useState(false);

  useEffect(() => {
    setDone(false);
    const timer = window.setTimeout(() => setDone(true), delayMs);
    return () => window.clearTimeout(timer);
  }, [delayMs, replayToken]);

  return done;
}

function ClaudeAetherArtwork({ darkMode, replayToken }: { darkMode: boolean; replayToken: number }) {
  const done = useDrawComplete(1200, replayToken);
  const tone = darkMode ? "#d4a27f" : "#D97757";

  return (
    <div className="relative h-[420px] w-[420px] lg:h-[520px] lg:w-[520px]">
      <svg viewBox="0 0 24 24" className="absolute left-1/2 top-1/2 h-[280px] w-[280px] -translate-x-1/2 -translate-y-1/2 lg:h-[340px] lg:w-[340px]">
        <path d={claudePath} className={`aether-claude-outline ${done ? "aether-claude-outline-done" : ""}`} stroke={tone} />
        <path d={claudePath} className={`aether-claude-fill ${done ? "aether-claude-fill-active" : ""}`} fill={tone} />
        <path d={claudePath} className={`aether-claude-ripple aether-claude-ripple-1 ${done ? "aether-claude-ripple-active" : ""}`} stroke={tone} />
        <path d={claudePath} className={`aether-claude-ripple aether-claude-ripple-2 ${done ? "aether-claude-ripple-active" : ""}`} stroke={tone} />
        <path d={claudePath} className={`aether-claude-ripple aether-claude-ripple-3 ${done ? "aether-claude-ripple-active" : ""}`} stroke={tone} />
      </svg>
    </div>
  );
}

function OpenAIAetherArtwork({ darkMode, replayToken }: { darkMode: boolean; replayToken: number }) {
  const done = useDrawComplete(1000, replayToken);
  const tone = darkMode ? "#f1ead8" : "#151518";

  return (
    <div className="relative h-[420px] w-[420px] lg:h-[520px] lg:w-[520px]">
      <svg viewBox="0 0 24 24" className="absolute left-1/2 top-1/2 h-[260px] w-[260px] -translate-x-1/2 -translate-y-1/2 lg:h-[330px] lg:w-[330px]">
        <g className={done ? "aether-openai-breathe" : ""} style={{ transformOrigin: "12px 12px" }}>
          <g className={done ? "aether-openai-rotate" : ""} style={{ transformOrigin: "12px 12px" }}>
            <path d={openaiPath} className={`aether-openai-outline ${done ? "aether-openai-outline-done" : ""}`} stroke={tone} />
            <path d={openaiPath} className={`aether-openai-fill ${done ? "aether-openai-fill-active" : ""}`} fill={tone} fillRule="evenodd" />
          </g>
        </g>
      </svg>
    </div>
  );
}

type GeminiStarLayer = "far" | "mid" | "near";

type GeminiStar = {
  id: number;
  layer: GeminiStarLayer;
  left: number;
  top: number;
  size: number;
  baseOpacity: number;
  twinkleDuration: number;
  twinkleDelay: number;
};

function createSeededRandom(seed: number) {
  let state = (seed >>> 0) || 1;
  return () => {
    state = (state * 1664525 + 1013904223) >>> 0;
    return state / 4294967296;
  };
}

function createGeminiStars(seed: number): GeminiStar[] {
  const random = createSeededRandom(seed + 101);
  let idCounter = 0;
  const stars: GeminiStar[] = [];

  const createLayer = (layer: GeminiStarLayer, count: number, sizeMin: number, sizeMax: number, opacityBase: number, opacityRange: number) => {
    for (let i = 0; i < count; i += 1) {
      const size = sizeMin + random() * (sizeMax - sizeMin);
      const padding = Math.max(4, size / 5);
      const minPos = padding;
      const maxPos = 100 - padding;
      stars.push({
        id: idCounter++,
        layer,
        left: minPos + random() * (maxPos - minPos),
        top: minPos + random() * (maxPos - minPos),
        size,
        baseOpacity: opacityBase + random() * opacityRange,
        twinkleDuration: 3 + random() * 4,
        twinkleDelay: random() * 5,
      });
    }
  };

  createLayer("far", 30, 6, 20, 0.2, 0.25);
  createLayer("mid", 18, 18, 42, 0.35, 0.35);
  createLayer("near", 10, 40, 85, 0.5, 0.35);

  return stars;
}

function GeminiAetherArtwork({ replayToken }: { replayToken: number }) {
  const gid = useId().replace(/:/g, "");
  const fill0 = `aether-gemini-fill-0-${gid}`;
  const fill1 = `aether-gemini-fill-1-${gid}`;
  const fill2 = `aether-gemini-fill-2-${gid}`;
  const maskId = `aether-gemini-mask-${gid}`;
  const baseGradient = `aether-gemini-base-${gid}`;
  const redOverlay = `aether-gemini-red-${gid}`;
  const yellowOverlay = `aether-gemini-yellow-${gid}`;
  const greenOverlay = `aether-gemini-green-${gid}`;
  const glowFilter = `aether-gemini-glow-${gid}`;
  const stars = useMemo(() => createGeminiStars(replayToken), [replayToken]);
  const [outlineComplete, setOutlineComplete] = useState(false);
  const [fillComplete, setFillComplete] = useState(false);
  const [fillRadius, setFillRadius] = useState(15);
  const [starsScattered, setStarsScattered] = useState(false);
  const fillAnimationRef = useRef<number | null>(null);

  useEffect(() => {
    const geminiOutlineDuration = 900;
    const geminiFillDuration = 600;
    setOutlineComplete(false);
    setFillComplete(false);
    setFillRadius(15);
    setStarsScattered(false);

    const scatterTimer = window.setTimeout(() => {
      setStarsScattered(true);
    }, 60);

    const outlineTimer = window.setTimeout(() => {
      const startRadius = 15;
      const endRadius = 0;
      const startTime = performance.now();

      const animate = (currentTime: number) => {
        const elapsed = currentTime - startTime;
        const progress = Math.min(elapsed / geminiFillDuration, 1);
        const easedProgress = 1 - Math.pow(1 - progress, 2);
        setFillRadius(startRadius - (startRadius - endRadius) * easedProgress);

        if (progress < 1) {
          fillAnimationRef.current = window.requestAnimationFrame(animate);
          return;
        }
        fillAnimationRef.current = null;
        setFillComplete(true);
        setOutlineComplete(true);
      };

      fillAnimationRef.current = window.requestAnimationFrame(animate);
    }, geminiOutlineDuration);

    return () => {
      window.clearTimeout(scatterTimer);
      window.clearTimeout(outlineTimer);
      if (fillAnimationRef.current !== null) {
        window.cancelAnimationFrame(fillAnimationRef.current);
        fillAnimationRef.current = null;
      }
    };
  }, [replayToken]);

  return (
    <div className="relative h-[420px] w-[420px] lg:h-[520px] lg:w-[520px]">
      <svg className="absolute h-0 w-0 overflow-hidden" aria-hidden="true">
        <defs>
          <linearGradient id={fill0} gradientUnits="userSpaceOnUse" x1="7" x2="11" y1="15.5" y2="12">
            <stop stopColor="#08B962" />
            <stop offset="1" stopColor="#08B962" stopOpacity="0" />
          </linearGradient>
          <linearGradient id={fill1} gradientUnits="userSpaceOnUse" x1="8" x2="11.5" y1="5.5" y2="11">
            <stop stopColor="#F94543" />
            <stop offset="1" stopColor="#F94543" stopOpacity="0" />
          </linearGradient>
          <linearGradient id={fill2} gradientUnits="userSpaceOnUse" x1="3.5" x2="17.5" y1="13.5" y2="12">
            <stop stopColor="#FABC12" />
            <stop offset=".46" stopColor="#FABC12" stopOpacity="0" />
          </linearGradient>
          <mask id={maskId}>
            <rect x="-4" y="-4" width="32" height="32" fill="white" />
            <circle cx="12" cy="12" r={fillRadius} fill="black" />
          </mask>

          <linearGradient id={baseGradient} x1="0%" y1="0%" x2="100%" y2="100%">
            <stop offset="0%" stopColor="#1A73E8" />
            <stop offset="50%" stopColor="#4285F4" />
            <stop offset="100%" stopColor="#669DF6" />
          </linearGradient>
          <linearGradient id={redOverlay} x1="50%" y1="0%" x2="50%" y2="50%">
            <stop offset="0%" stopColor="#EA4335" />
            <stop offset="100%" stopColor="#EA4335" stopOpacity="0" />
          </linearGradient>
          <linearGradient id={yellowOverlay} x1="0%" y1="50%" x2="50%" y2="50%">
            <stop offset="0%" stopColor="#FBBC04" />
            <stop offset="100%" stopColor="#FBBC04" stopOpacity="0" />
          </linearGradient>
          <linearGradient id={greenOverlay} x1="50%" y1="100%" x2="50%" y2="50%">
            <stop offset="0%" stopColor="#34A853" />
            <stop offset="100%" stopColor="#34A853" stopOpacity="0" />
          </linearGradient>
          <filter id={glowFilter} x="-50%" y="-50%" width="200%" height="200%">
            <feGaussianBlur stdDeviation="2" result="blur" />
            <feFlood floodColor="#4285F4" floodOpacity="0.28" />
            <feComposite in2="blur" operator="in" />
            <feMerge>
              <feMergeNode />
              <feMergeNode in="SourceGraphic" />
            </feMerge>
          </filter>
        </defs>
      </svg>

      <div className="aether-gemini-stars">
        {stars.map((star) => (
          <span
            key={`gemini-star-${star.id}`}
            className={`aether-gemini-star-wrap aether-gemini-star-${star.layer} ${starsScattered ? "aether-gemini-star-wrap-active" : ""}`}
            style={
              {
                width: `${star.size}px`,
                height: `${star.size}px`,
                "--target-left": `${star.left}%`,
                "--target-top": `${star.top}%`,
              } as CSSProperties
            }
          >
            <span
              className="aether-gemini-star-core"
              style={
                {
                  "--base-opacity": String(star.baseOpacity),
                  "--twinkle-delay": `${star.twinkleDelay}s`,
                  "--twinkle-duration": `${star.twinkleDuration}s`,
                } as CSSProperties
              }
            >
              <svg viewBox="0 0 24 24" className="aether-gemini-star-svg" style={{ filter: `url(#${glowFilter})` }}>
                <path d={geminiStarPath} fill={`url(#${baseGradient})`} />
                <path d={geminiStarPath} fill={`url(#${redOverlay})`} />
                <path d={geminiStarPath} fill={`url(#${yellowOverlay})`} />
                <path d={geminiStarPath} fill={`url(#${greenOverlay})`} />
              </svg>
            </span>
          </span>
        ))}
      </div>

      <div className="absolute left-1/2 top-1/2 h-[280px] w-[280px] -translate-x-1/2 -translate-y-1/2 lg:h-[340px] lg:w-[340px]">
        <svg viewBox="-4 -4 32 32" className="h-full w-full">
          <g className={`aether-gemini-outline-group ${outlineComplete ? "aether-gemini-outline-group-done" : ""}`}>
            <path d={geminiPath} className="aether-gemini-outline" stroke="#3186FF" />
            <path d={geminiPath} className="aether-gemini-outline" stroke={`url(#${fill0})`} />
            <path d={geminiPath} className="aether-gemini-outline" stroke={`url(#${fill1})`} />
            <path d={geminiPath} className="aether-gemini-outline" stroke={`url(#${fill2})`} />
          </g>

          <g className="aether-gemini-fill" mask={fillComplete ? undefined : `url(#${maskId})`}>
            <path d={geminiPath} fill="#3186FF" />
            <path d={geminiPath} fill={`url(#${fill0})`} />
            <path d={geminiPath} fill={`url(#${fill1})`} />
            <path d={geminiPath} fill={`url(#${fill2})`} />
          </g>

          <g className={`aether-gemini-ripple aether-gemini-ripple-1 ${fillComplete ? "aether-gemini-ripple-active" : ""}`}>
            <path d={geminiPath} fill="#3186FF" />
            <path d={geminiPath} fill={`url(#${fill0})`} />
            <path d={geminiPath} fill={`url(#${fill1})`} />
            <path d={geminiPath} fill={`url(#${fill2})`} />
          </g>
          <g className={`aether-gemini-ripple aether-gemini-ripple-2 ${fillComplete ? "aether-gemini-ripple-active" : ""}`}>
            <path d={geminiPath} fill="#3186FF" />
            <path d={geminiPath} fill={`url(#${fill0})`} />
            <path d={geminiPath} fill={`url(#${fill1})`} />
            <path d={geminiPath} fill={`url(#${fill2})`} />
          </g>
          <g className={`aether-gemini-ripple aether-gemini-ripple-3 ${fillComplete ? "aether-gemini-ripple-active" : ""}`}>
            <path d={geminiPath} fill="#3186FF" />
            <path d={geminiPath} fill={`url(#${fill0})`} />
            <path d={geminiPath} fill={`url(#${fill1})`} />
            <path d={geminiPath} fill={`url(#${fill2})`} />
          </g>
        </svg>
      </div>
    </div>
  );
}

export default function AetherCliArtwork({ id, darkMode, replayToken }: AetherCliArtworkProps) {
  return (
    <>
      <style>{`
        @keyframes aether-openai-rotate { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }
        @keyframes aether-openai-breathe {
          0%, 100% { transform: scale(1); filter: brightness(1); }
          50% { transform: scale(1.03); filter: brightness(1.05); }
        }
        @keyframes aether-ripple {
          0% { opacity: 0.36; transform: scale(1); }
          100% { opacity: 0; transform: scale(1.22); }
        }
        @keyframes aether-gemini-outline-draw {
          0% { stroke-dashoffset: 100; opacity: 0; }
          5% { opacity: 0.5; }
          15% { opacity: 1; }
          100% { stroke-dashoffset: 0; opacity: 1; }
        }
        @keyframes aether-gemini-ripple-expand {
          0% { opacity: 0.22; transform: scale(1); }
          100% { opacity: 0; transform: scale(1.24); }
        }
        @keyframes aether-gemini-ripple-expand-up {
          0% { opacity: 0.2; transform: translate(0, 0) scale(1); }
          100% { opacity: 0; transform: translate(0, -8px) scale(1.2); }
        }
        @keyframes aether-gemini-ripple-expand-diagonal {
          0% { opacity: 0.2; transform: translate(0, 0) scale(1); }
          100% { opacity: 0; transform: translate(6px, -6px) scale(1.18); }
        }
        @keyframes aether-gemini-star-twinkle {
          0% { opacity: 0; transform: scale(0.52); }
          50% { opacity: var(--base-opacity); transform: scale(1); filter: brightness(1.2); }
          100% { opacity: 0; transform: scale(0.52); }
        }

        .aether-openai-outline,
        .aether-claude-outline {
          fill: none;
          stroke-width: 0.52px;
          vector-effect: non-scaling-stroke;
          stroke-dasharray: 220;
          stroke-dashoffset: 220;
          animation: aether-stroke-draw 1s ease-out forwards;
        }

        @keyframes aether-stroke-draw {
          0% { stroke-dashoffset: 220; opacity: 0.25; }
          20% { opacity: 1; }
          100% { stroke-dashoffset: 0; opacity: 1; }
        }

        .aether-openai-outline-done,
        .aether-claude-outline-done {
          opacity: 0;
          transition: opacity 0.28s ease-out;
        }

        .aether-openai-fill,
        .aether-claude-fill {
          opacity: 0;
        }

        .aether-openai-fill-active,
        .aether-claude-fill-active {
          opacity: 1;
          transition: opacity 0.42s ease-out;
        }

        .aether-openai-rotate { animation: aether-openai-rotate 25s linear infinite; }
        .aether-openai-breathe { animation: aether-openai-breathe 3.5s ease-in-out infinite; }

        .aether-claude-ripple {
          fill: none;
          stroke-width: 0.46px;
          vector-effect: non-scaling-stroke;
          opacity: 0;
          transform-origin: 12.6px 12.7px;
        }

        .aether-claude-ripple-active { animation: aether-ripple 4s cubic-bezier(0, 0, 0.2, 1) infinite; }
        .aether-claude-ripple-2 { animation-delay: 1.3s; }
        .aether-claude-ripple-3 { animation-delay: 2.6s; }

        .aether-gemini-outline-group { opacity: 1; }
        .aether-gemini-outline-group-done { opacity: 0; transition: opacity 0.3s ease-out; }

        .aether-gemini-outline {
          fill: none;
          stroke-width: 1px;
          vector-effect: non-scaling-stroke;
          stroke-dasharray: 100;
          stroke-dashoffset: 100;
          animation: aether-gemini-outline-draw 1.8s cubic-bezier(0.4, 0, 0.2, 1) forwards;
        }

        .aether-gemini-fill { opacity: 1; }
        .aether-gemini-ripple {
          opacity: 0;
          pointer-events: none;
          transform-origin: 12px 12px;
        }
        .aether-gemini-ripple-active { animation: aether-gemini-ripple-expand 4s cubic-bezier(0, 0, 0.2, 1) infinite; }
        .aether-gemini-ripple-1 { animation-delay: 0s; }
        .aether-gemini-ripple-2 { animation-name: aether-gemini-ripple-expand-up; animation-delay: 1.3s; }
        .aether-gemini-ripple-3 { animation-name: aether-gemini-ripple-expand-diagonal; animation-delay: 2.6s; }

        .aether-gemini-stars {
          position: absolute;
          inset: 0;
          pointer-events: none;
          perspective: 800px;
        }

        .aether-gemini-star-wrap {
          position: absolute;
          left: 50%;
          top: 50%;
          opacity: 0;
          transform: translate(-50%, -50%) scale(0.3);
          transition:
            opacity 0.8s ease-out,
            transform 0.8s ease-out,
            left 2s cubic-bezier(0.16, 1, 0.3, 1),
            top 2s cubic-bezier(0.16, 1, 0.3, 1);
        }

        .aether-gemini-star-wrap-active {
          left: var(--target-left);
          top: var(--target-top);
          opacity: 1;
          transform: translate(-50%, -50%) scale(1);
        }

        .aether-gemini-star-core {
          display: block;
          width: 100%;
          height: 100%;
          opacity: 0;
          animation: aether-gemini-star-twinkle var(--twinkle-duration) ease-in-out infinite;
          animation-delay: calc(var(--twinkle-delay) + 0.8s);
        }

        .aether-gemini-star-far { filter: blur(0.5px); z-index: 1; }
        .aether-gemini-star-mid { filter: blur(0.2px); z-index: 2; }
        .aether-gemini-star-near { filter: none; z-index: 3; }

        .aether-gemini-star-wrap-active .aether-gemini-star-core {
          opacity: 1;
        }

        .aether-gemini-star-svg {
          width: 100%;
          height: 100%;
          overflow: visible;
        }
      `}</style>

      {id === "claude" && <ClaudeAetherArtwork darkMode={darkMode} replayToken={replayToken} />}
      {id === "openai" && <OpenAIAetherArtwork darkMode={darkMode} replayToken={replayToken} />}
      {id === "gemini" && <GeminiAetherArtwork replayToken={replayToken} />}
    </>
  );
}
