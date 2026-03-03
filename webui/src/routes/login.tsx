import { useEffect, useMemo, useRef, useState, type CSSProperties } from "react";
import { useNavigate } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import iconSvg from "@/assets/icon.svg";
import { ChevronDown, ChevronLeft, Layers, Megaphone, Puzzle, Rocket, Sparkles, SunMoon } from "lucide-react";
import CliSection from "./login/CliSection";
import OrvionLineLogo from "./login/OrvionLineLogo";
import AetherCliArtwork from "./login/AetherCliArtwork";
import { cliSections, sectionTabs, type SectionId } from "./login/home-config";
import { clearStoredAuthToken, getStoredAuthToken, setStoredAuthToken } from "@/lib/auth";

const SECTION_INDEX: Record<SectionId, number> = {
  home: 0,
  claude: 1,
  openai: 2,
  gemini: 3,
  more: 4,
};

const FEATURE_CARDS = [
  {
    icon: Layers,
    title: "Claude / OpenAI / Gemini",
    desc: "已完整接入三大主流 AI 编程助手的标准 API",
    status: "completed" as const,
  },
  {
    icon: Puzzle,
    title: "格式转换",
    desc: "支持接口格式转换与自定义请求头能力",
    status: "completed" as const,
  },
  {
    icon: Rocket,
    title: "协同开发",
    desc: "远程开发、Skill 分享与 Playground 正在开发中",
    status: "in-progress" as const,
  },
];

const getDirectionMultiplier = (index: number): number => {
  if (index === SECTION_INDEX.claude || index === SECTION_INDEX.gemini) return 1;
  if (index === SECTION_INDEX.openai) return -1;
  return 0;
};

const getHorizontalOffset = (index: number, distance: number, progress: number): number => {
  const direction = getDirectionMultiplier(index);
  if (direction === 0) return 0;
  return (1 - progress) * distance * direction;
};

export default function LoginPage() {
  const [token, setToken] = useState("");
  const [loginError, setLoginError] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [showLoginForm, setShowLoginForm] = useState(false);
  const [currentSection, setCurrentSection] = useState(SECTION_INDEX.home);
  const [sectionVisibility, setSectionVisibility] = useState<number[]>([1, 0, 0, 0, 0]);
  const [windowWidth, setWindowWidth] = useState<number>(() => window.innerWidth);
  const [darkMode, setDarkMode] = useState(false);
  const [logoReplayToken, setLogoReplayToken] = useState(0);
  const navigate = useNavigate();
  const hasToken = getStoredAuthToken().length > 0;
  const scrollRootRef = useRef<HTMLDivElement | null>(null);
  const sectionRefs = useRef<Record<SectionId, HTMLElement | null>>({
    home: null,
    claude: null,
    openai: null,
    gemini: null,
    more: null,
  });
  const currentSectionRef = useRef(SECTION_INDEX.home);
  const rafRef = useRef<number | null>(null);

  useEffect(() => {
    const root = scrollRootRef.current;
    if (!root) return;

    const calculateVisibility = (element: HTMLElement | null): number => {
      if (!element) return 0;
      const rect = element.getBoundingClientRect();
      const containerHeight = window.innerHeight;
      if (rect.bottom < 0 || rect.top > containerHeight) return 0;
      const elementCenter = rect.top + rect.height / 2;
      const viewportCenter = containerHeight / 2;
      const distanceFromCenter = Math.abs(elementCenter - viewportCenter);
      const maxDistance = containerHeight / 2 + rect.height / 2;
      return Math.max(0, 1 - distanceFromCenter / maxDistance);
    };

    const runScrollCompute = () => {
      const newScrollTop = root.scrollTop;

      const nextVisibility = sectionTabs.map((item) => calculateVisibility(sectionRefs.current[item.id]));
      setSectionVisibility(nextVisibility);

      const scrollMiddle = newScrollTop + window.innerHeight / 2;
      let nextSection = SECTION_INDEX.home;
      for (let i = sectionTabs.length - 1; i >= 0; i -= 1) {
        const sectionId = sectionTabs[i].id;
        const sectionEl = sectionRefs.current[sectionId];
        if (sectionEl && sectionEl.offsetTop <= scrollMiddle) {
          nextSection = i;
          break;
        }
      }

      if (nextSection !== currentSectionRef.current) {
        setCurrentSection(nextSection);
        currentSectionRef.current = nextSection;
        if (nextSection >= SECTION_INDEX.claude && nextSection <= SECTION_INDEX.gemini) {
          setLogoReplayToken((prev) => prev + 1);
        }
      }
    };

    const handleScroll = () => {
      if (rafRef.current !== null) return;
      rafRef.current = window.requestAnimationFrame(() => {
        rafRef.current = null;
        runScrollCompute();
      });
    };

    const handleResize = () => {
      setWindowWidth(window.innerWidth);
      runScrollCompute();
    };

    root.addEventListener("scroll", handleScroll, { passive: true });
    window.addEventListener("resize", handleResize, { passive: true });
    window.requestAnimationFrame(runScrollCompute);

    return () => {
      root.removeEventListener("scroll", handleScroll);
      window.removeEventListener("resize", handleResize);
      if (rafRef.current !== null) {
        window.cancelAnimationFrame(rafRef.current);
      }
    };
  }, []);

  const handleLogin = async (e: React.FormEvent) => {
    e.preventDefault();
    if (submitting) return;
    const normalized = setStoredAuthToken(token);
    if (!normalized) {
      setLoginError("访问令牌不能为空");
      return;
    }

    setSubmitting(true);
    setLoginError("");

    try {
      const response = await fetch("/api/version", {
        method: "GET",
        headers: {
          Authorization: `Bearer ${normalized}`,
        },
      });

      if (response.status === 401 || response.status === 403) {
        clearStoredAuthToken();
        setLoginError("访问令牌无效或已过期，请检查后重试");
        return;
      }

      if (!response.ok) {
        setLoginError(`登录校验失败（HTTP ${response.status}）`);
        return;
      }

      navigate("/", { replace: true });
    } catch {
      setLoginError("登录校验请求失败，请确认服务可用后重试");
    } finally {
      setSubmitting(false);
    }
  };

  const bindSection = (id: SectionId) => (el: HTMLElement | null) => {
    sectionRefs.current[id] = el;
  };

  const scrollToSection = (id: SectionId) => {
    sectionRefs.current[id]?.scrollIntoView({ behavior: "smooth", block: "start" });
  };

  const openLogin = () => {
    if (hasToken) {
      navigate("/", { replace: true });
      return;
    }
    setLoginError("");
    setShowLoginForm(true);
  };

  const copyText = async (value: string) => {
    try {
      await navigator.clipboard.writeText(value);
    } catch (error) {
      console.error(error);
    }
  };

  const getBadgeStyle = (index: number): CSSProperties => {
    const visibility = sectionVisibility[index] ?? 0;
    const opacity = Math.min(1, visibility * 3);
    const progress = Math.min(1, visibility * 2);
    const offsetX = getHorizontalOffset(index, 24, progress);
    const offsetY = getDirectionMultiplier(index) === 0 ? (1 - progress) * 8 : 0;
    return { opacity, transform: `translate(${offsetX}px, ${offsetY}px)` };
  };

  const getTitleStyle = (index: number): CSSProperties => {
    const visibility = sectionVisibility[index] ?? 0;
    const adjustedVisibility = Math.max(0, visibility - 0.1) / 0.9;
    const progress = Math.min(1, adjustedVisibility * 2);
    const yBase = getDirectionMultiplier(index) === 0 ? 30 : 10;
    const offsetY = (1 - progress) * yBase;
    const offsetX = getHorizontalOffset(index, 32, progress);
    return { opacity: progress, transform: `translate(${offsetX}px, ${offsetY}px)` };
  };

  const getDescStyle = (index: number): CSSProperties => {
    const visibility = sectionVisibility[index] ?? 0;
    const adjustedVisibility = Math.max(0, visibility - 0.2) / 0.8;
    const progress = Math.min(1, adjustedVisibility * 2);
    const yBase = getDirectionMultiplier(index) === 0 ? 30 : 8;
    const offsetY = (1 - progress) * yBase;
    const offsetX = getHorizontalOffset(index, 28, progress);
    return { opacity: progress, transform: `translate(${offsetX}px, ${offsetY}px)` };
  };

  const getButtonsStyle = (index: number): CSSProperties => {
    const visibility = sectionVisibility[index] ?? 0;
    const adjustedVisibility = Math.max(0, visibility - 0.3) / 0.7;
    const progress = Math.min(1, adjustedVisibility * 2);
    const yBase = getDirectionMultiplier(index) === 0 ? 20 : 8;
    const offsetY = (1 - progress) * yBase;
    const offsetX = getHorizontalOffset(index, 24, progress);
    return { opacity: progress, transform: `translate(${offsetX}px, ${offsetY}px)` };
  };

  const getScrollIndicatorStyle = (index: number): CSSProperties => {
    const visibility = sectionVisibility[index] ?? 0;
    const adjustedVisibility = Math.max(0, visibility - 0.4) / 0.6;
    const opacity = Math.min(1, adjustedVisibility * 2);
    return { opacity };
  };

  const getCardStyle = (sectionIndex: number, cardIndex: number): CSSProperties => {
    const visibility = sectionVisibility[sectionIndex] ?? 0;
    const totalDelay = 0.25 + cardIndex * 0.1;
    const adjustedVisibility = Math.max(0, visibility - totalDelay) / (1 - totalDelay);
    const progress = Math.min(1, adjustedVisibility * 2);
    const offsetY = (1 - progress) * 10;
    const offsetX = getHorizontalOffset(sectionIndex, 20, progress);
    return { opacity: progress, transform: `translate(${offsetX}px, ${offsetY}px)` };
  };

  const getFeatureCardStyle = (sectionIndex: number, cardIndex: number): CSSProperties => {
    const visibility = sectionVisibility[sectionIndex] ?? 0;
    const totalDelay = 0.2 + cardIndex * 0.15;
    const adjustedVisibility = Math.max(0, visibility - totalDelay) / (1 - totalDelay);
    const opacity = Math.min(1, adjustedVisibility * 2);
    const offsetY = (1 - Math.min(1, adjustedVisibility * 2)) * 30;
    const scale = 0.9 + Math.min(1, adjustedVisibility * 2) * 0.1;
    return { opacity, transform: `translateY(${offsetY}px) scale(${scale})` };
  };

  const fixedLogoStyle = useMemo<CSSProperties>(() => {
    const isDesktop = windowWidth >= 768;
    const desktopLeftOffset = "26vw";
    const desktopRightOffset = "20vw";
    const desktopCliLift = "-6vh";
    if (currentSection === SECTION_INDEX.home) {
      return {
        transform: "scale(1.1) translateY(-12vh)",
        opacity: 0.25,
        transition: "transform 0.8s cubic-bezier(0.4, 0, 0.2, 1), opacity 0.6s ease-out",
      };
    }
    if (currentSection === SECTION_INDEX.claude) {
      return {
        transform: isDesktop
          ? `translate(calc(-1 * ${desktopLeftOffset}), ${desktopCliLift}) scale(1)`
          : "translateY(-30vh) scale(0.6)",
        opacity: isDesktop ? 1 : 0.2,
        transition: "transform 0.8s cubic-bezier(0.4, 0, 0.2, 1), opacity 0.6s ease-out",
      };
    }
    if (currentSection === SECTION_INDEX.openai) {
      return {
        transform: isDesktop
          ? `translate(${desktopRightOffset}, ${desktopCliLift}) scale(1)`
          : "translateY(-30vh) scale(0.6)",
        opacity: isDesktop ? 1 : 0.2,
        transition: "transform 0.8s cubic-bezier(0.4, 0, 0.2, 1), opacity 0.6s ease-out",
      };
    }
    if (currentSection === SECTION_INDEX.gemini) {
      return {
        transform: isDesktop
          ? `translate(calc(-1 * ${desktopLeftOffset}), ${desktopCliLift}) scale(1)`
          : "translateY(-30vh) scale(0.6)",
        opacity: isDesktop ? 1 : 0.2,
        transition: "transform 0.8s cubic-bezier(0.4, 0, 0.2, 1), opacity 0.6s ease-out",
      };
    }
    return {
      transform: isDesktop ? "translateX(0) scale(1)" : "translateY(-20vh) scale(0.8)",
      opacity: 0.15,
      transition: "transform 0.8s cubic-bezier(0.4, 0, 0.2, 1), opacity 0.6s ease-out",
    };
  }, [currentSection, windowWidth]);

  const currentSectionId = sectionTabs[currentSection]?.id ?? "home";
  const currentLogoId =
    currentSection === SECTION_INDEX.claude
      ? "claude"
      : currentSection === SECTION_INDEX.openai
        ? "openai"
        : currentSection === SECTION_INDEX.gemini
          ? "gemini"
          : null;

  const pageClass = darkMode ? "bg-[#191714] text-[#e3e0d3]" : "bg-[#fafaf7] text-[#191919]";
  const headerClass = darkMode ? "border-[rgba(227,224,211,0.12)] bg-[#191714]/95" : "border-[#cc785c]/10 bg-[#fafaf7]/90";
  const normalTextClass = darkMode ? "text-[#c9c3b4]" : "text-[#666663]";
  const panelClass = darkMode ? "bg-[#262624]/80 border-[rgba(227,224,211,0.12)]" : "bg-white/90 border-[#e5e4df]";
  const codePanelClass = darkMode ? "bg-[#1f1d1a]" : "bg-[#f8f6f1]";
  const gridBg = darkMode
    ? "linear-gradient(rgba(227,224,211,0.05) 1px, transparent 1px), linear-gradient(90deg, rgba(227,224,211,0.05) 1px, transparent 1px)"
    : "linear-gradient(rgba(25,25,25,0.035) 1px, transparent 1px), linear-gradient(90deg, rgba(25,25,25,0.035) 1px, transparent 1px)";

  return (
    <div className={`relative ${pageClass}`}>
      <style>{`
        .scroll-indicator {
          position: fixed;
          right: 2rem;
          top: 50%;
          transform: translateY(-50%);
          z-index: 9999;
          display: flex;
          flex-direction: column;
          gap: 0.75rem;
        }
        @media (max-width: 1023px) {
          .scroll-indicator { display: none; }
        }
        .scroll-indicator-btn {
          position: relative;
          display: flex;
          align-items: center;
          justify-content: flex-end;
          padding: 0.25rem;
        }
        .scroll-indicator-label {
          position: absolute;
          right: 1.5rem;
          font-size: 0.75rem;
          font-weight: 500;
          color: #666663;
          opacity: 0;
          transition: opacity 0.2s ease;
          white-space: nowrap;
          background: rgba(255, 255, 255, 0.9);
          backdrop-filter: blur(8px);
          padding: 0.25rem 0.5rem;
          border-radius: 0.25rem;
          pointer-events: none;
        }
        .scroll-indicator-btn:hover .scroll-indicator-label { opacity: 1; }
        .scroll-indicator-dot {
          width: 10px;
          height: 10px;
          border-radius: 50%;
          border: 2px solid #d4d4d4;
          background: transparent;
          transition: all 0.3s ease;
        }
        .scroll-indicator-dot.active {
          background: #cc785c;
          border-color: #cc785c;
          transform: scale(1.3);
        }
        .logo-container {
          width: 320px;
          height: 320px;
          position: relative;
          display: flex;
          align-items: center;
          justify-content: center;
        }
        .logo-container.home-section {
          width: 400px;
          height: 400px;
        }
        .logo-container > :not(style) {
          position: absolute;
          inset: 0;
          display: flex;
          align-items: center;
          justify-content: center;
        }
        @media (max-width: 768px) {
          .logo-container { width: 240px; height: 240px; }
          .logo-container.home-section { width: 280px; height: 280px; }
        }
      `}</style>

      <nav className="scroll-indicator">
        {sectionTabs.map((item) => {
          const isActive = currentSectionId === item.id;
          return (
            <button key={item.id} className="scroll-indicator-btn group" onClick={() => scrollToSection(item.id)}>
              <span className="scroll-indicator-label">{item.label}</span>
              <div className={`scroll-indicator-dot ${isActive ? "active" : ""}`} />
            </button>
          );
        })}
      </nav>

      <div
        ref={scrollRootRef}
        className={`relative z-10 h-screen overflow-y-auto overflow-x-hidden snap-y snap-mandatory scroll-smooth ${
          darkMode ? "bg-[#191714]/95" : "bg-[#fafaf7]/95"
        }`}
        style={{
          backgroundImage: gridBg,
          backgroundSize: "28px 28px",
        }}
      >
        <header className={`sticky top-0 z-50 border-b backdrop-blur-xl transition-all ${headerClass}`}>
          <div className="mx-auto flex h-14 max-w-[1320px] items-center justify-between px-3 sm:h-[72px] sm:px-6">
            <button className="flex h-9 w-9 items-center justify-center sm:h-10 sm:w-10" onClick={() => scrollToSection("home")}>
              <img src={iconSvg} alt="Orvion" className="h-[3.9375rem] w-[3.9375rem] sm:h-[5.0625rem] sm:w-[5.0625rem]" />
            </button>

            <nav className="mx-6 hidden items-center gap-1 lg:flex">
              {sectionTabs.slice(0, 4).map((item) => {
                const isActive = currentSectionId === item.id;
                return (
                  <button
                    key={item.id}
                    onClick={() => scrollToSection(item.id)}
                    className={`group relative px-3 py-2 text-sm font-medium transition ${
                      isActive
                        ? "text-[#cc785c]"
                        : darkMode
                          ? "text-[#c9c3b4] hover:text-white"
                          : "text-[#666663] hover:text-[#191919]"
                    }`}
                  >
                    {item.label}
                    <span
                      className={`absolute bottom-0 left-0 right-0 h-0.5 rounded-full transition ${
                        isActive ? "bg-[#cc785c] scale-x-100" : "scale-x-0 bg-transparent"
                      }`}
                    />
                  </button>
                );
              })}

              <a
                href="https://fawney19.github.io/Aether/guide"
                target="_blank"
                rel="noopener noreferrer"
                className={`group relative px-3 py-2 text-sm font-medium transition ${
                  darkMode ? "text-[#c9c3b4] hover:text-white" : "text-[#666663] hover:text-[#191919]"
                }`}
              >
                文档
              </a>

              <button
                onClick={() => scrollToSection("more")}
                className={`group relative px-3 py-2 text-sm font-medium transition ${
                  currentSectionId === "more"
                    ? "text-[#cc785c]"
                    : darkMode
                      ? "text-[#c9c3b4] hover:text-white"
                      : "text-[#666663] hover:text-[#191919]"
                }`}
              >
                更多
                <span
                  className={`absolute bottom-0 left-0 right-0 h-0.5 rounded-full transition ${
                    currentSectionId === "more" ? "bg-[#cc785c] scale-x-100" : "scale-x-0 bg-transparent"
                  }`}
                />
              </button>
            </nav>

            <div className="flex items-center gap-1.5 sm:gap-2">
              <button
                onClick={openLogin}
                className="min-w-[64px] rounded-xl bg-[#cc785c] px-3 py-1.5 text-xs font-medium text-white shadow-lg shadow-[#cc785c]/30 transition hover:bg-[#d4a27f] sm:min-w-[72px] sm:px-4 sm:py-2 sm:text-sm"
              >
                {hasToken ? "进入" : "登录"}
              </button>
              <button
                className={`flex h-8 w-8 items-center justify-center rounded-lg transition sm:h-9 sm:w-9 ${
                  darkMode
                    ? "text-[#c9c3b4] hover:bg-[#2e2a25] hover:text-white"
                    : "text-[#666663] hover:bg-[#f0f0eb] hover:text-[#191919]"
                }`}
                onClick={() => setDarkMode((prev) => !prev)}
                title={darkMode ? "切换浅色" : "切换深色"}
              >
                <SunMoon className="h-3.5 w-3.5 sm:h-4 sm:w-4" />
              </button>
              <button
                className={`flex h-8 w-8 items-center justify-center rounded-lg transition sm:h-9 sm:w-9 ${
                  darkMode
                    ? "text-[#c9c3b4] hover:bg-[#2e2a25] hover:text-white"
                    : "text-[#666663] hover:bg-[#f0f0eb] hover:text-[#191919]"
                }`}
                title="公告"
              >
                <Megaphone className="h-3.5 w-3.5 sm:h-4 sm:w-4" />
              </button>
            </div>
          </div>
        </header>

        <main className="relative z-10">
          <div className="fixed bottom-0 left-0 right-0 top-0 z-20 flex items-center justify-center overflow-hidden pointer-events-none">
            <div className={`transform-gpu logo-container ${currentSection === SECTION_INDEX.home ? "home-section" : ""}`} style={fixedLogoStyle}>
              {currentSection === SECTION_INDEX.home ? (
                <OrvionLineLogo darkMode={darkMode} />
              ) : currentLogoId ? (
                <AetherCliArtwork id={currentLogoId} darkMode={darkMode} replayToken={logoReplayToken} />
              ) : (
                <img src={iconSvg} alt="Orvion" className="h-[220px] w-[220px] opacity-75 lg:h-[280px] lg:w-[280px]" />
              )}
            </div>
          </div>

          <section
            id="home"
            ref={bindSection("home")}
            className="min-h-screen snap-start flex items-center justify-center px-4 py-20 sm:px-8 md:px-16 lg:px-20"
          >
            <div className="mx-auto max-w-4xl text-center">
              <div className="mb-8 mt-6 flex h-64 w-full items-center justify-center sm:mb-10 sm:h-80 md:h-[26rem]" />
              <h1 className="mb-6 text-3xl font-bold leading-tight sm:text-5xl md:text-7xl" style={getTitleStyle(SECTION_INDEX.home)}>
                欢迎使用{" "}
                <span className="text-[#cc785c]">
                  Orvion<span className="animate-pulse">_</span>
                </span>
              </h1>
              <p className={`mx-auto mb-8 max-w-2xl text-base sm:text-lg md:text-xl ${normalTextClass}`} style={getDescStyle(SECTION_INDEX.home)}>
                AI 开发工具统一接入平台
                <br />
                整合 Claude Code、Codex CLI、Gemini CLI 等多个 AI 编程助手
              </p>
              <button className="mt-8 transition hover:scale-110" style={getScrollIndicatorStyle(SECTION_INDEX.home)} onClick={() => scrollToSection("claude")}>
                <ChevronDown className={`mx-auto h-8 w-8 animate-bounce ${normalTextClass}`} />
              </button>
            </div>
          </section>

          {cliSections.map((item) => {
            const sectionIndex = SECTION_INDEX[item.id];
            return (
              <CliSection
                key={item.id}
                item={item}
                darkMode={darkMode}
                normalTextClass={normalTextClass}
                panelClass={panelClass}
                codePanelClass={codePanelClass}
                onCopy={copyText}
                sectionRef={bindSection(item.id)}
                badgeStyle={getBadgeStyle(sectionIndex)}
                titleStyle={getTitleStyle(sectionIndex)}
                descStyle={getDescStyle(sectionIndex)}
                cardStyleFn={(cardIndex) => getCardStyle(sectionIndex, cardIndex)}
              />
            );
          })}

          <section
            id="more"
            ref={bindSection("more")}
            className="relative min-h-screen snap-start overflow-hidden px-4 py-12 sm:px-8 md:px-16 lg:px-20 md:py-20"
          >
            <div className="relative z-10 mx-auto max-w-4xl text-center">
              <div
                className="mb-4 inline-flex items-center gap-2 rounded-full border border-[#cc785c]/20 bg-[#cc785c]/10 px-4 py-2 text-sm font-medium text-[#cc785c]"
                style={getBadgeStyle(SECTION_INDEX.more)}
              >
                <Sparkles className="h-4 w-4" />
                项目进度
              </div>
              <h2 className="mb-3 text-2xl font-bold md:text-5xl" style={getTitleStyle(SECTION_INDEX.more)}>
                功能开发进度
              </h2>
              <p className={`mx-auto mb-6 max-w-2xl text-base md:mb-12 md:text-lg ${normalTextClass}`} style={getDescStyle(SECTION_INDEX.more)}>
                核心 API 代理功能已完成，正在载入更多能力
              </p>

              <div className="grid gap-3 md:grid-cols-3 md:gap-6">
                {FEATURE_CARDS.map((feature, idx) => {
                  const IconComp = feature.icon;
                  return (
                    <div
                      key={feature.title}
                      className={`rounded-2xl border p-4 md:p-6 ${
                        feature.status === "completed" ? panelClass : `${panelClass} border-dashed`
                      }`}
                      style={getFeatureCardStyle(SECTION_INDEX.more, idx)}
                    >
                      <div className="mx-auto mb-2 flex h-10 w-10 items-center justify-center rounded-xl bg-[#cc785c]/8 md:mb-4 md:h-12 md:w-12">
                        <IconComp className={`h-5 w-5 text-[#cc785c] md:h-6 md:w-6 ${feature.status !== "completed" ? "opacity-60" : ""}`} />
                      </div>
                      <h3 className={`mb-1 text-base font-bold md:mb-2 md:text-lg ${feature.status === "completed" ? "" : normalTextClass}`}>
                        {feature.title}
                      </h3>
                      <p className={`text-xs md:text-sm ${normalTextClass}`}>{feature.desc}</p>
                      <div
                        className={`mt-2 inline-flex items-center rounded-full border px-2.5 py-1 text-xs font-medium md:mt-3 ${
                          feature.status === "completed"
                            ? "border-[#cc785c]/20 bg-[#cc785c]/5 text-[#cc785c]"
                            : darkMode
                              ? "border-[rgba(227,224,211,0.18)] text-[#a8a198]"
                              : "border-[#e5e4df] text-[#91918d]"
                        }`}
                      >
                        {feature.status === "completed" ? "已完成" : "开发中"}
                      </div>
                    </div>
                  );
                })}
              </div>

              <div className="relative z-30 mt-6 flex items-center justify-center gap-4 md:mt-12" style={getButtonsStyle(SECTION_INDEX.more)}>
                <button
                  className="inline-flex w-[160px] items-center justify-center gap-2 rounded-xl border-2 border-[#cc785c] bg-transparent px-6 py-3 text-base font-semibold text-[#cc785c] transition hover:scale-105 hover:bg-[#cc785c]/10"
                  onClick={openLogin}
                >
                  <Rocket className="h-5 w-5" />
                  立即开始
                </button>
              </div>
            </div>
          </section>
        </main>
      </div>

      {showLoginForm && (
        <div className="fixed inset-0 z-[120] flex items-center justify-center bg-black/45 px-4 backdrop-blur-sm">
          <Card className={`w-full max-w-md ${darkMode ? "border-[rgba(227,224,211,0.12)] bg-[#24211d] text-[#f1ead8]" : "border-[#e5e4df] bg-white text-[#191919]"} shadow-2xl`}>
            <CardHeader>
              <Button
                variant="ghost"
                className={`-ml-2 mb-2 h-8 w-fit px-2 ${darkMode ? "hover:bg-[#312c26]" : "hover:bg-[#f0f0eb]"}`}
                onClick={() => setShowLoginForm(false)}
              >
                <ChevronLeft className="mr-1 h-4 w-4" />
                返回主页
              </Button>
              <CardTitle className="text-2xl">登录</CardTitle>
              <CardDescription className={normalTextClass}>输入访问令牌进入系统</CardDescription>
            </CardHeader>
            <form onSubmit={handleLogin}>
              <CardContent className="grid gap-4">
                <div className="grid gap-2">
                  <Label htmlFor="token">访问令牌</Label>
                  <Input
                    id="token"
                    type="password"
                    value={token}
                    onChange={(e) => {
                      setToken(e.target.value);
                      if (loginError) setLoginError("");
                    }}
                    placeholder="输入您的访问令牌"
                    required
                    className={darkMode ? "border-[rgba(227,224,211,0.18)] bg-[#1a1714]" : ""}
                  />
                  {loginError && (
                    <p className="text-xs text-red-500">{loginError}</p>
                  )}
                </div>
              </CardContent>
              <CardFooter>
                <Button
                  className="mt-5 w-full bg-[#cc785c] text-white hover:bg-[#d4a27f]"
                  type="submit"
                  disabled={submitting}
                >
                  {submitting ? "校验中..." : "登录"}
                </Button>
              </CardFooter>
            </form>
          </Card>
        </div>
      )}
    </div>
  );
}
