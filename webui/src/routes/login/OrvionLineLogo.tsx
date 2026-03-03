import { useEffect, useMemo, useRef, useState, type CSSProperties } from "react";
import iconSvg from "@/assets/icon.svg";
import iconSvgRaw from "@/assets/icon.svg?raw";

type OrvionLineLogoProps = {
  darkMode: boolean;
};

const extractSvgMeta = (raw: string) => {
  const pathMatch = raw.match(/<path d="([^"]+)"/s);
  const transformMatch = raw.match(/<g transform="([^"]+)"/s);
  const path = pathMatch?.[1] ?? "";
  const transform = transformMatch?.[1] ?? "translate(-200,1000) scale(0.150000,-0.150000)";
  return { path, transform };
};

export default function OrvionLineLogo({ darkMode }: OrvionLineLogoProps) {
  const [cycleSeed, setCycleSeed] = useState(0);
  const pathRef = useRef<SVGPathElement | null>(null);
  const [pathLength, setPathLength] = useState(18000);

  const { path, transform } = useMemo(() => extractSvgMeta(iconSvgRaw), []);
  const strokeColor = darkMode ? "#d4a27f" : "#cc785c";
  const glowColor = darkMode ? "rgba(212,162,127,0.38)" : "rgba(204,120,92,0.34)";

  useEffect(() => {
    const timer = window.setInterval(() => {
      setCycleSeed((prev) => prev + 1);
    }, 5600);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => {
    const node = pathRef.current;
    if (!node) return;
    try {
      const len = node.getTotalLength();
      if (Number.isFinite(len) && len > 0) {
        setPathLength(len);
      }
    } catch {
      setPathLength(18000);
    }
  }, [cycleSeed, path]);

  return (
    <div className="relative h-full w-full">
      <style>{`
        @keyframes orvion-logo-stroke-draw {
          0% {
            opacity: 0.18;
            stroke-dashoffset: var(--orvion-path-len);
          }
          12% { opacity: 1; }
          100% {
            opacity: 1;
            stroke-dashoffset: 0;
          }
        }

        @keyframes orvion-logo-fill-reveal {
          0% { opacity: 0; }
          100% { opacity: 1; }
        }

        @keyframes orvion-logo-breathe {
          0%, 100% { transform: scale(1); }
          50% { transform: scale(1.025); }
        }

        @keyframes orvion-logo-image-reveal {
          0% { opacity: 0; }
          100% { opacity: 1; }
        }
      `}</style>

      <svg viewBox="0 0 800 800" className="absolute inset-0 h-full w-full">
        <g transform={transform}>
          <path
            d={path}
            fill="none"
            stroke={strokeColor}
            strokeWidth="8"
            strokeLinecap="round"
            strokeLinejoin="round"
            style={{ opacity: 0.16 }}
          />
        </g>
      </svg>

      <svg
        key={`orvion-generate-${cycleSeed}`}
        viewBox="0 0 800 800"
        className="absolute inset-0 h-full w-full"
        style={{ animation: "orvion-logo-breathe 4.2s ease-in-out 1.9s infinite" }}
      >
        <defs>
          <filter id="orvion-glow" x="-50%" y="-50%" width="200%" height="200%">
            <feGaussianBlur stdDeviation="6" result="blur" />
            <feFlood floodColor={glowColor} />
            <feComposite in2="blur" operator="in" />
            <feMerge>
              <feMergeNode />
              <feMergeNode in="SourceGraphic" />
            </feMerge>
          </filter>
        </defs>

        <g transform={transform}>
          <path
            ref={pathRef}
            d={path}
            fill="none"
            stroke={strokeColor}
            strokeWidth="8"
            strokeLinecap="round"
            strokeLinejoin="round"
            filter="url(#orvion-glow)"
            style={
              {
                "--orvion-path-len": `${pathLength}`,
                strokeDasharray: `${pathLength}`,
                strokeDashoffset: `${pathLength}`,
                animation: "orvion-logo-stroke-draw 1.45s cubic-bezier(0.4, 0, 0.2, 1) forwards",
              } as CSSProperties
            }
          />
          <path
            d={path}
            fill={strokeColor}
            fillRule="nonzero"
            style={{ opacity: 0, animation: "orvion-logo-fill-reveal 0.7s ease-out 1.05s forwards" }}
          />
        </g>
      </svg>

      <div
        key={`orvion-image-${cycleSeed}`}
        className="pointer-events-none absolute inset-0"
        style={
          {
            backgroundColor: strokeColor,
            WebkitMaskImage: `url(${iconSvg})`,
            maskImage: `url(${iconSvg})`,
            WebkitMaskRepeat: "no-repeat",
            maskRepeat: "no-repeat",
            WebkitMaskPosition: "center",
            maskPosition: "center",
            WebkitMaskSize: "contain",
            maskSize: "contain",
            opacity: 0,
            animation: "orvion-logo-image-reveal 0.6s ease-out 1.18s forwards",
          } as CSSProperties
        }
      />
    </div>
  );
}
