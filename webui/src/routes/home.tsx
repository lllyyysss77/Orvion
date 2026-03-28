"use client"

import { useState, useEffect, memo, useCallback, useMemo } from "react";
import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import Loading from "@/components/loading";
import {
  getMetricsSummary,
  getAuthKeySummary,
  getRequestAmountTrend,
  getDailyModelCostTrend
} from "@/lib/api";
import type { AuthKeySummary, DailyModelCostSummary, MetricsSummary, RequestAmountSummary } from "@/lib/api";
import { getStoredAuthToken } from "@/lib/auth";
import { toast } from "sonner";
import {
  RefreshCw,
  Activity,
  MessageSquare,
  Clock,
  BadgeCheck,
  ArrowDownToLine,
  ArrowUpToLine,
  CalendarDays,
  Coins,
  CheckCircle2,
  XCircle,
  type LucideIcon,
} from "lucide-react";

const cardHoverClass =
  "transition-all duration-200 ease-out will-change-transform hover:-translate-y-0.5 hover:shadow-md hover:border-primary/30";

const summaryCardClass =
  "relative min-h-[64px] overflow-hidden rounded-[20px] border border-border/50 bg-card/80 shadow-[0_6px_18px_rgba(0,0,0,0.08)] backdrop-blur-sm";

const summarySideTitleClass =
  "text-[9px] font-semibold tracking-[0.22em] text-muted-foreground/80";

const summaryTitleIconClass =
  "size-7 rounded-lg bg-emerald-50 text-emerald-700 flex items-center justify-center shrink-0 ring-1 ring-emerald-100/80 dark:bg-emerald-400/10 dark:text-emerald-200 dark:ring-emerald-400/20";

const summaryMetricIconClass =
  "size-6 rounded-lg bg-emerald-50 text-emerald-700 flex items-center justify-center shrink-0 ring-1 ring-emerald-100/80 dark:bg-emerald-400/10 dark:text-emerald-200 dark:ring-emerald-400/20";

const AUTH_KEY_PREFIXES = ["sk-github.com/racio/orvion-", "sk-github.com/racio/llmio-"];

const formatFixedNumber = (value: number | null | undefined, digits = 2) => {
  if (value == null || !Number.isFinite(value)) {
    return "--";
  }
  return value.toFixed(digits);
};

const formatMoney = (value: number | null | undefined, digits?: number) => {
  if (value == null || !Number.isFinite(value)) {
    return "--";
  }
  if (typeof digits === "number") {
    return `$${value.toFixed(digits)}`;
  }
  const abs = Math.abs(value);
  if (abs === 0) return "$0";
  if (abs >= 1) return `$${value.toFixed(2).replace(/\.?0+$/, "")}`;
  if (abs >= 0.01) return `$${value.toFixed(4).replace(/\.?0+$/, "")}`;
  return `$${value.toFixed(6).replace(/\.?0+$/, "")}`;
};

const formatDate = (value: string | null | undefined) => {
  if (!value) return "--";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "--";
  return date.toISOString().slice(0, 10);
};

const formatSeconds = (valueMs: number | null | undefined) => {
  if (valueMs == null || !Number.isFinite(valueMs)) {
    return "--";
  }
  return `${(valueMs / 1000).toFixed(2)} s`;
};


// Animated counter component
const AnimatedCounter = ({
  value,
  duration = 1000,
  className,
}: {
  value: number;
  duration?: number;
  className?: string;
}) => {
  const [count, setCount] = useState(0);

  useEffect(() => {
    let startTime: number | null = null;
    const animateCount = (timestamp: number) => {
      if (!startTime) startTime = timestamp;
      const progress = timestamp - startTime;
      const progressRatio = Math.min(progress / duration, 1);
      const currentValue = Math.floor(progressRatio * value);

      setCount(currentValue);

      if (progress < duration) {
        requestAnimationFrame(animateCount);
      }
    };

    requestAnimationFrame(animateCount);
  }, [value, duration]);

  return (
    <div className={["text-3xl font-bold", className].filter(Boolean).join(" ")}>
      {count.toLocaleString()}
    </div>
  );
};

type SummaryMetric = {
  label: string;
  value: React.ReactNode;
  subLabel?: string;
  icon: LucideIcon;
};

type SummaryCardProps = {
  title: string;
  icon: LucideIcon;
  items: [SummaryMetric, SummaryMetric];
};

const SummaryCard = memo(({ title, icon: TitleIcon, items }: SummaryCardProps) => {
  return (
    <Card className={summaryCardClass}>
      <div className="flex h-full">
        <div className="w-11 shrink-0 flex flex-col items-center justify-center gap-1 py-1">
          <span className={summaryTitleIconClass} aria-hidden="true">
            <TitleIcon className="size-3.5" />
          </span>
          <span
            className={summarySideTitleClass}
            style={{ writingMode: "vertical-rl" }}
          >
            {title}
          </span>
        </div>
        <div className="w-px bg-border/60 my-1.5" />
        <div className="flex-1 grid grid-rows-2 gap-0.5 px-1.5 py-0.5">
          {items.map((item) => (
            <div key={item.label} className="flex items-center gap-2">
              <span className={summaryMetricIconClass} aria-hidden="true">
                <item.icon className="size-3" />
              </span>
              <div className="min-w-0">
                <div className="text-[10px] text-muted-foreground">{item.label}</div>
                <div className="text-[13px] font-semibold leading-tight">{item.value}</div>
                {item.subLabel && (
                  <div className="text-[9px] text-muted-foreground">
                    {item.subLabel}
                  </div>
                )}
              </div>
            </div>
          ))}
        </div>
      </div>
    </Card>
  );
});

const buildCurvePoints = (data: number[], width: number, height: number) => {
  if (data.length < 2) return [];
  const min = Math.min(...data);
  const max = Math.max(...data);
  const range = max - min || 1;
  const step = width / (data.length - 1);
  return data.map((value, index) => {
    const x = step * index;
    const y = height - ((value - min) / range) * height;
    return { x, y };
  });
};

type TodayAmountTrendCardProps = {
  totalRequests: number;
  totalAmount: number;
  rangeLabel: string;
  rangeValue: string;
  curvePoints: RequestAmountPointView[];
};

type RequestAmountPointView = {
  hour: number;
  requests: number;
  amount: number;
};

const buildAreaCurve = (data: number[], width: number, height: number) => {
  const points = buildCurvePoints(data, width, height);
  if (points.length === 0) return { line: "", area: "" };
  const line = buildSmoothLine(points);
  const area = `${line} L ${points[points.length - 1].x.toFixed(2)} ${height} L ${points[0].x.toFixed(2)} ${height} Z`;
  return { line, area };
};

const buildSmoothLine = (points: { x: number; y: number }[]) => {
  if (points.length < 2) return "";
  if (points.length === 2) {
    return `M${points[0].x.toFixed(2)} ${points[0].y.toFixed(2)} L${points[1].x.toFixed(2)} ${points[1].y.toFixed(2)}`;
  }
  const tension = 1;
  const path: string[] = [`M${points[0].x.toFixed(2)} ${points[0].y.toFixed(2)}`];
  for (let i = 0; i < points.length - 1; i += 1) {
    const p0 = points[i - 1] ?? points[i];
    const p1 = points[i];
    const p2 = points[i + 1];
    const p3 = points[i + 2] ?? p2;

    const cp1x = p1.x + (p2.x - p0.x) / 6 * tension;
    const cp1y = p1.y + (p2.y - p0.y) / 6 * tension;
    const cp2x = p2.x - (p3.x - p1.x) / 6 * tension;
    const cp2y = p2.y - (p3.y - p1.y) / 6 * tension;

    path.push(
      `C${cp1x.toFixed(2)} ${cp1y.toFixed(2)} ${cp2x.toFixed(2)} ${cp2y.toFixed(2)} ${p2.x.toFixed(2)} ${p2.y.toFixed(2)}`
    );
  }
  return path.join(" ");
};

type DailyModelCostCardProps = {
  trend: DailyModelCostSummary;
};

const modelCostPalette = [
  "#5B8FF9",
  "#F4664A",
  "#5AD8A6",
  "#F6BD16",
  "#9270CA",
  "#269A99",
  "#FF9D4D",
  "#6DC8EC",
];

const formatModelDisplayName = (model: string) => {
  const name = (model || "").trim();
  if (!name) return "unknown";
  if (name.toLowerCase() === "others") return "其他模型";
  return name;
};

const TodayAmountTrendCard = memo(({
  totalRequests,
  totalAmount,
  rangeLabel,
  rangeValue,
  curvePoints,
}: TodayAmountTrendCardProps) => {
  const chartWidth = 520;
  const chartHeight = 120;
  const [hoveredIndex, setHoveredIndex] = useState<number | null>(null);
  const curveData = useMemo(
    () => curvePoints.map((point) => point.amount),
    [curvePoints]
  );
  const chartPoints = useMemo(
    () => buildCurvePoints(curveData, chartWidth, chartHeight),
    [curveData, chartWidth, chartHeight]
  );
  const chart = useMemo(
    () => buildAreaCurve(curveData, chartWidth, chartHeight),
    [curveData, chartWidth, chartHeight]
  );
  const safeHoveredIndex = hoveredIndex == null || chartPoints.length === 0
    ? null
    : Math.max(0, Math.min(hoveredIndex, chartPoints.length - 1));
  const hoveredPoint = safeHoveredIndex == null ? null : chartPoints[safeHoveredIndex];
  const hoveredData = safeHoveredIndex == null ? null : curvePoints[safeHoveredIndex];
  const tooltipLeft = hoveredPoint ? `${(hoveredPoint.x / chartWidth) * 100}%` : "0%";

  return (
    <Card className={`${cardHoverClass} gap-3 h-full min-h-[460px]`}>
      <CardHeader className="pb-2">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="flex flex-wrap items-center gap-6 text-xs text-muted-foreground">
            <div className="flex flex-col gap-1">
              <span>今日消耗金额</span>
              <span className="text-lg font-semibold text-foreground">{formatMoney(totalAmount)}</span>
            </div>
            <div className="flex flex-col gap-1">
              <span>请求次数</span>
              <span className="text-lg font-semibold text-foreground">{formatCompactValue(totalRequests)}</span>
            </div>
          </div>
          <div className="text-xs text-muted-foreground">
            <span className="mr-1">{rangeLabel}</span>
            <span className="text-foreground">{rangeValue}</span>
          </div>
        </div>
      </CardHeader>
      <CardContent className="pt-0 flex-1">
        <div className="relative flex h-full flex-col overflow-hidden rounded-2xl border border-border/40 bg-card/60 px-4 py-3">
          <div className="pointer-events-none absolute inset-0 opacity-30" style={{ backgroundImage: "radial-gradient(circle at 10% 20%, rgba(34,197,94,0.28), transparent 55%)" }} />
          <div className="flex items-center justify-between text-[10px] text-muted-foreground">
            <span>0:00</span>
            <span>6:00</span>
            <span>12:00</span>
            <span>18:00</span>
            <span>23:00</span>
          </div>
          <div className="mt-2 flex-1">
            <svg
              viewBox={`0 0 ${chartWidth} ${chartHeight}`}
              className="h-full min-h-[210px] w-full"
              onMouseLeave={() => setHoveredIndex(null)}
              onMouseMove={(event) => {
                if (curvePoints.length <= 1) return;
                const rect = event.currentTarget.getBoundingClientRect();
                if (rect.width <= 0) return;
                const ratio = Math.max(0, Math.min(1, (event.clientX - rect.left) / rect.width));
                const index = Math.round(ratio * (curvePoints.length - 1));
                setHoveredIndex(index);
              }}
            >
              <path d={chart.area} className="fill-cyan-200/60" />
              <path d={chart.line} className="stroke-cyan-600" fill="none" strokeWidth="2" />
              {hoveredPoint && (
                <>
                  <line
                    x1={hoveredPoint.x}
                    y1={0}
                    x2={hoveredPoint.x}
                    y2={chartHeight}
                    className="stroke-cyan-500/50"
                    strokeDasharray="4 4"
                  />
                  <circle
                    cx={hoveredPoint.x}
                    cy={hoveredPoint.y}
                    r="4"
                    className="fill-cyan-600 stroke-card"
                    strokeWidth="2"
                  />
                </>
              )}
            </svg>
          </div>
          {hoveredData && (
            <div
              className="pointer-events-none absolute top-7 z-10 -translate-x-1/2 rounded-lg border border-border/70 bg-card px-2 py-1 text-[11px] shadow"
              style={{ left: tooltipLeft }}
            >
              <div className="text-muted-foreground">{hoveredData.hour.toString().padStart(2, "0")}:00</div>
              <div className="font-semibold text-foreground">金额 ${formatAmountValue(hoveredData.amount)}</div>
              <div className="font-semibold text-foreground">请求 {hoveredData.requests}</div>
            </div>
          )}
        </div>
      </CardContent>
    </Card>
  );
});

const DailyModelCostCard = memo(({ trend }: DailyModelCostCardProps) => {
  const [hoveredIndex, setHoveredIndex] = useState<number | null>(null);
  const chartWidth = 640;
  const chartHeight = 320;
  const margin = { top: 14, right: 16, bottom: 36, left: 58 };
  const plotWidth = chartWidth - margin.left - margin.right;
  const plotHeight = chartHeight - margin.top - margin.bottom;

  const seriesWithColor = useMemo(
    () => trend.series.map((item, index) => ({
      ...item,
      color: modelCostPalette[index % modelCostPalette.length],
    })),
    [trend.series]
  );

  const axisMax = DAILY_MODEL_COST_AXIS_MAX;
  const yTicks = DAILY_MODEL_COST_Y_TICKS;

  const groupWidth = trend.labels.length > 0 ? plotWidth / trend.labels.length : plotWidth;
  const barWidth = Math.min(44, Math.max(16, groupWidth * 0.58));

  const getY = (value: number) => margin.top + plotHeight - (value / axisMax) * plotHeight;

  const tooltip = useMemo(() => {
    if (hoveredIndex == null) return null;
    if (hoveredIndex < 0 || hoveredIndex >= trend.labels.length) return null;
    const breakdown = seriesWithColor
      .map((item) => ({
        model: formatModelDisplayName(item.model),
        amount: item.amounts[hoveredIndex] ?? 0,
        color: item.color,
      }))
      .filter((item) => item.amount > 0)
      .sort((a, b) => b.amount - a.amount);

    return {
      label: trend.labels[hoveredIndex],
      total: trend.totals[hoveredIndex] ?? 0,
      breakdown,
      exceedAxisMax: (trend.totals[hoveredIndex] ?? 0) > axisMax,
    };
  }, [axisMax, hoveredIndex, seriesWithColor, trend.labels, trend.totals]);

  return (
    <Card className={`${cardHoverClass} gap-3 h-full min-h-[460px]`}>
      <CardHeader className="pb-2">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="flex flex-col gap-1">
            <span className="text-xs text-muted-foreground">每日模型成本</span>
          </div>
          <div className="text-xs text-muted-foreground">
            最近 <span className="text-foreground">{trend.labels.length}</span> 天
          </div>
        </div>
      </CardHeader>
      <CardContent className="pt-0 flex-1">
        {seriesWithColor.length === 0 ? (
          <div className="flex h-full items-center justify-center rounded-2xl border border-dashed border-border/50 px-4 py-10 text-center text-xs text-muted-foreground">
            暂无模型成本数据
          </div>
        ) : (
          <div className="relative flex h-full flex-col overflow-hidden rounded-2xl border border-border/40 bg-card/60 px-3 py-3">
            <div className="pointer-events-none absolute inset-0 opacity-20" style={{ backgroundImage: "radial-gradient(circle at 20% 10%, rgba(59,130,246,0.25), transparent 60%)" }} />
            <div className="relative z-10 flex flex-col">
              <svg
                viewBox={`0 0 ${chartWidth} ${chartHeight}`}
                className="h-[290px] w-full"
              >
                {yTicks.map((tick) => {
                  const y = getY(tick);
                  return (
                    <g key={`y-${tick}`}>
                      <line
                        x1={margin.left}
                        y1={y}
                        x2={margin.left + plotWidth}
                        y2={y}
                        className="stroke-border/45"
                      />
                      <text
                        x={margin.left - 8}
                        y={y + 4}
                        textAnchor="end"
                        className="fill-muted-foreground text-[12px]"
                      >
                        {formatAxisTick(tick)}
                      </text>
                    </g>
                  );
                })}

                <line
                  x1={margin.left}
                  y1={margin.top}
                  x2={margin.left}
                  y2={margin.top + plotHeight}
                  className="stroke-border/65"
                />
                <line
                  x1={margin.left}
                  y1={margin.top + plotHeight}
                  x2={margin.left + plotWidth}
                  y2={margin.top + plotHeight}
                  className="stroke-border/65"
                />

                <text
                  transform={`translate(${18} ${margin.top + plotHeight / 2}) rotate(-90)`}
                  textAnchor="middle"
                  className="fill-muted-foreground text-[13px]"
                >
                  费用 ($)
                </text>

                {trend.labels.map((label, index) => {
                  const slotX = margin.left + groupWidth * index;
                  const x = slotX + (groupWidth - barWidth) / 2;
                  let offset = 0;

                  return (
                    <g key={`${label}-${index}`}>
                      {seriesWithColor.map((item) => {
                        const amount = item.amounts[index] ?? 0;
                        if (amount <= 0) return null;
                        const remain = Math.max(0, axisMax - offset);
                        const drawAmount = Math.min(amount, remain);
                        if (drawAmount <= 0) {
                          offset += amount;
                          return null;
                        }
                        const y = getY(offset + drawAmount);
                        const height = Math.max((drawAmount / axisMax) * plotHeight, 1);
                        offset += amount;
                        return (
                          <rect
                            key={`${item.model}-${index}`}
                            x={x}
                            y={y}
                            width={barWidth}
                            height={height}
                            rx={4}
                            fill={item.color}
                            fillOpacity={0.92}
                          />
                        );
                      })}

                      <rect
                        x={slotX}
                        y={margin.top}
                        width={groupWidth}
                        height={plotHeight}
                        fill="transparent"
                        onMouseEnter={() => setHoveredIndex(index)}
                        onFocus={() => setHoveredIndex(index)}
                        onMouseLeave={() => setHoveredIndex(null)}
                        onBlur={() => setHoveredIndex(null)}
                      />

                      <text
                        x={slotX + groupWidth / 2}
                        y={margin.top + plotHeight + 18}
                        textAnchor="middle"
                        className="fill-muted-foreground text-[13px]"
                      >
                        {label}
                      </text>
                    </g>
                  );
                })}
              </svg>

              <div className="mt-1 shrink-0 flex flex-wrap items-center justify-center gap-x-3 gap-y-1.5 text-[12px] font-medium text-foreground/85">
                {seriesWithColor.map((item) => (
                  <span key={item.model} className="inline-flex items-center gap-1.5">
                    <span
                      className="inline-block size-2.5 rounded-sm"
                      style={{ backgroundColor: item.color }}
                    />
                    {formatModelDisplayName(item.model)}
                  </span>
                ))}
              </div>
            </div>

            {tooltip && tooltip.breakdown.length > 0 && (
              <div className="pointer-events-none absolute right-3 top-3 z-20 w-52 rounded-xl border border-border/70 bg-card/95 px-3 py-2 text-xs shadow-md backdrop-blur-sm">
                <div className="font-semibold text-foreground">{tooltip.label}</div>
                <div className="mt-1 text-muted-foreground">Total: {formatMoney(tooltip.total, 4)}</div>
                {tooltip.exceedAxisMax && (
                  <div className="mt-1 text-[10px] text-amber-600">
                    超过坐标上限 $60，柱体已封顶显示
                  </div>
                )}
                <div className="mt-2 space-y-1">
                  {tooltip.breakdown.map((item) => (
                    <div key={item.model} className="flex items-center justify-between gap-2">
                      <span className="inline-flex items-center gap-1.5 min-w-0">
                        <span className="size-2 rounded-sm shrink-0" style={{ backgroundColor: item.color }} />
                        <span className="truncate">{item.model}</span>
                      </span>
                      <span className="font-medium text-foreground">${formatAmountValue(item.amount)}</span>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
        )}
      </CardContent>
    </Card>
  );
});

type AuthKeyDashboardProps = {
  summary: AuthKeySummary | null;
  errorMessage?: string | null;
};

const AuthKeyDashboard = ({ summary, errorMessage }: AuthKeyDashboardProps) => {
  const name = summary?.name?.trim() || "未命名";
  const keyMasked = summary?.keyMasked || "--";
  const expiresAt = formatDate(summary?.expiresAt);
  const expireInDays = summary?.expireInDays;
  const expireText = summary?.expiresAt
    ? `${expireInDays ?? 0} 天后到期`
    : "长期有效";
  const totalCost = summary?.totalCost ?? 0;
  const costMax = Math.max(totalCost, 30);
  const costProgress = costMax > 0 ? Math.min(totalCost / costMax, 1) : 0;
  const allowedModels = summary?.allowAll ? ["全部模型"] : (summary?.models || []);

  return (
    <div className="space-y-4">
      {errorMessage ? (
        <Card className="rounded-2xl border border-border/60 bg-card/90 shadow-sm">
          <CardContent className="py-6 text-sm text-muted-foreground">
            {errorMessage}
          </CardContent>
        </Card>
      ) : null}

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-[1.2fr_1fr]">
        <Card className="rounded-2xl border border-border/60 bg-card/90 shadow-sm">
          <CardContent className="p-4 space-y-4">
            <div className="flex items-start justify-between gap-3">
              <div>
                <div className="text-sm font-semibold">名称</div>
                <div className="mt-2 rounded-xl border border-border/60 bg-muted/40 px-3 py-2 text-sm font-mono">
                  {keyMasked}
                </div>
              </div>
              <div className="text-xs text-muted-foreground">{name}</div>
            </div>
            <div className="flex flex-wrap items-center gap-3 text-xs text-muted-foreground">
              <span>过期时间</span>
              <span className="text-foreground">{expiresAt}</span>
              <span className="rounded-full bg-emerald-50 px-2 py-0.5 text-[11px] text-emerald-700">
                {expireText}
              </span>
            </div>
          </CardContent>
        </Card>

        <Card className="rounded-2xl border border-border/60 bg-card/90 shadow-sm">
          <CardContent className="p-4 space-y-3">
            <div className="flex items-start justify-between">
              <div>
                <div className="text-sm text-muted-foreground">消耗费用</div>
                <div className="mt-1 text-4xl font-semibold text-emerald-600">
                  {formatMoney(summary?.totalCost)}
                </div>
              </div>
              <Coins className="size-8 text-muted-foreground/40" />
            </div>
            <div className="space-y-1">
              <div className="h-2 rounded-full bg-muted/50">
                <div
                  className="h-2 rounded-full bg-emerald-500/70"
                  style={{ width: `${costProgress * 100}%` }}
                />
              </div>
              <div className="flex items-center justify-between text-[11px] text-muted-foreground">
                <span>0</span>
                <span>∞</span>
              </div>
            </div>
          </CardContent>
        </Card>
      </div>

      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <Card className="rounded-2xl border border-border/60 bg-card/90 shadow-sm">
          <CardContent className="p-4 space-y-1">
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <CheckCircle2 className="size-4 text-emerald-500" />
              成功请求
            </div>
            <div className="text-xl font-semibold">{formatFixedNumber(summary?.successRequests)}</div>
          </CardContent>
        </Card>
        <Card className="rounded-2xl border border-border/60 bg-card/90 shadow-sm">
          <CardContent className="p-4 space-y-1">
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <XCircle className="size-4 text-rose-500" />
              失败请求
            </div>
            <div className="text-xl font-semibold">{formatFixedNumber(summary?.failureRequests)}</div>
          </CardContent>
        </Card>
        <Card className="rounded-2xl border border-border/60 bg-card/90 shadow-sm">
          <CardContent className="p-4 space-y-1">
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <Activity className="size-4 text-emerald-500" />
              请求次数
            </div>
            <div className="text-xl font-semibold">{formatFixedNumber(summary?.totalRequests)}</div>
          </CardContent>
        </Card>
        <Card className="rounded-2xl border border-border/60 bg-card/90 shadow-sm">
          <CardContent className="p-4 space-y-1">
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <Clock className="size-4 text-amber-500" />
              消耗时间
            </div>
            <div className="text-xl font-semibold">{formatSeconds(summary?.totalTimeMs)}</div>
          </CardContent>
        </Card>
      </div>

      <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
        <Card className="rounded-2xl border border-border/60 bg-card/90 shadow-sm">
          <CardContent className="p-4 space-y-3">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2 text-sm font-semibold">
                <ArrowDownToLine className="size-4 text-emerald-600" />
                消耗 Token
              </div>
              <div className="text-sm font-semibold">
                {formatFixedNumber(summary?.totalTokens)}
              </div>
            </div>
            <div className="grid grid-cols-2 gap-4 text-xs text-muted-foreground">
              <div className="space-y-1">
                <div>输入 Tokens</div>
                <div className="text-base font-semibold text-foreground">
                  {formatFixedNumber(summary?.promptTokens)}
                </div>
              </div>
              <div className="space-y-1">
                <div>输出 Tokens</div>
                <div className="text-base font-semibold text-foreground">
                  {formatFixedNumber(summary?.completionTokens)}
                </div>
              </div>
            </div>
          </CardContent>
        </Card>

        <Card className="rounded-2xl border border-border/60 bg-card/90 shadow-sm">
          <CardContent className="p-4 space-y-3">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2 text-sm font-semibold">
                <Coins className="size-4 text-emerald-600" />
                消耗费用
              </div>
              <div className="text-sm font-semibold">
                {formatMoney(summary?.totalCost)}
              </div>
            </div>
            <div className="grid grid-cols-2 gap-4 text-xs text-muted-foreground">
              <div className="space-y-1">
                <div>输入费用</div>
                <div className="text-base font-semibold text-foreground">
                  {formatMoney(summary?.inputCost)}
                </div>
              </div>
              <div className="space-y-1">
                <div>输出费用</div>
                <div className="text-base font-semibold text-foreground">
                  {formatMoney(summary?.outputCost)}
                </div>
              </div>
            </div>
          </CardContent>
        </Card>
      </div>

      <Card className="rounded-2xl border border-border/60 bg-card/90 shadow-sm">
        <CardContent className="p-4 space-y-3">
          <div className="flex items-center gap-2 text-sm font-semibold">
            <BadgeCheck className="size-4 text-emerald-600" />
            支持的模型
          </div>
          {allowedModels.length > 0 ? (
            <div className="flex flex-wrap gap-2">
              {allowedModels.map((model) => (
                <Badge key={model} variant="secondary" className="rounded-full">
                  {model}
                </Badge>
              ))}
            </div>
          ) : (
            <div className="text-xs text-muted-foreground">暂无模型</div>
          )}
        </CardContent>
      </Card>
    </div>
  );
};

const defaultRequestAmount: RequestAmountSummary = {
  total_requests: 0,
  total_amount: 0,
  range: "today",
  points: [],
};

const defaultDailyModelCost: DailyModelCostSummary = {
  range: "daily",
  dates: [],
  labels: [],
  totals: [],
  series: [],
};

const formatCompactValue = (value: number) => {
  if (!Number.isFinite(value)) return "0";
  const abs = Math.abs(value);
  const format = (num: number, suffix: string) => {
    const fixed = num.toFixed(2).replace(/\.?0+$/, "");
    return `${fixed}${suffix}`;
  };
  if (abs >= 1_000_000) return format(value / 1_000_000, "M");
  if (abs >= 1_000) return format(value / 1_000, "k");
  return value.toString();
};

const formatAmountValue = (value: number) => {
  if (!Number.isFinite(value)) return "0";
  const abs = Math.abs(value);
  if (abs === 0) return "0";
  if (abs >= 1) return value.toFixed(2).replace(/\.?0+$/, "");
  if (abs >= 0.01) return value.toFixed(4).replace(/\.?0+$/, "");
  return value.toFixed(6).replace(/\.?0+$/, "");
};

const DAILY_MODEL_COST_AXIS_MAX = 60;
const DAILY_MODEL_COST_Y_TICKS = [0, 10, 20, 30, 40, 50, 60];

const formatAxisTick = (value: number) => {
  if (!Number.isFinite(value)) return "0";
  return value.toFixed(0);
};

type HomeHeaderProps = {
  title: string;
  onRefresh: () => void;
};

const HomeHeader = memo(({ title, onRefresh }: HomeHeaderProps) => {
  return (
    <div className="flex flex-col gap-2 flex-shrink-0">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <h2 className="text-2xl font-bold tracking-tight">{title}</h2>
        </div>
        <Button
          onClick={onRefresh}
          variant="outline"
          size="icon"
          className="ml-auto shrink-0"
          aria-label="刷新概览"
          title="刷新概览"
        >
          <RefreshCw className="size-4" />
        </Button>
      </div>
    </div>
  );
});

export default function Home() {
  const [loading, setLoading] = useState(true);
  const [requestAmount, setRequestAmount] = useState<RequestAmountSummary>(defaultRequestAmount);
  const [dailyModelCost, setDailyModelCost] = useState<DailyModelCostSummary>(defaultDailyModelCost);
  const [authKeySummary, setAuthKeySummary] = useState<AuthKeySummary | null>(null);
  const [authKeyError, setAuthKeyError] = useState<string | null>(null);
  const [authKeyMode, setAuthKeyMode] = useState(false);

  // Real data from APIs
  const [summary, setSummary] = useState<MetricsSummary>({
    totalReqs: 0,
    successRate: 0,
    promptTokens: 0,
    completionTokens: 0,
    totalTokens: 0,
    todayTokens: 0,
    totalAmount: 0,
    todayAmount: 0,
    todayReqs: 0,
    todaySuccessRate: 0,
    todaySuccessReqs: 0,
    todayFailureReqs: 0,
    totalSuccessReqs: 0,
    totalFailureReqs: 0,
  });

  const fetchAuthKeySummary = useCallback(async () => {
    try {
      const data = await getAuthKeySummary();
      setAuthKeySummary(data);
      setAuthKeyError(null);
      return true;
    } catch (err) {
      setAuthKeySummary(null);
      const message = err instanceof Error ? err.message : String(err);
      setAuthKeyError(`获取 API Key 概览失败: ${message}`);
      return false;
    }
  }, []);
  const fetchSummary = useCallback(async () => {
    try {
      const data = await getMetricsSummary();
      setSummary(data);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`获取系统概览失败: ${message}`);
      console.error(err);
    }
  }, []);

  const fetchRequestAmount = useCallback(async () => {
    try {
      const data = await getRequestAmountTrend();
      setRequestAmount(data);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`获取请求金额趋势失败: ${message}`);
      console.error(err);
      setRequestAmount(defaultRequestAmount);
    }
  }, []);

  const fetchDailyModelCost = useCallback(async () => {
    try {
      const data = await getDailyModelCostTrend(7, 5);
      setDailyModelCost(data);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      toast.error(`获取每日模型成本失败: ${message}`);
      console.error(err);
      setDailyModelCost(defaultDailyModelCost);
    }
  }, []);

  const load = useCallback(async () => {
    setLoading(true);
    const token = getStoredAuthToken();
    const isAuthKeyToken = AUTH_KEY_PREFIXES.some((prefix) => token.startsWith(prefix));

    if (isAuthKeyToken) {
      setAuthKeyMode(true);
      await fetchAuthKeySummary();
      setLoading(false);
      return;
    }

    setAuthKeyMode(false);
    setAuthKeySummary(null);
    setAuthKeyError(null);
    await Promise.all([fetchSummary(), fetchRequestAmount(), fetchDailyModelCost()]);
    setLoading(false);
  }, [fetchAuthKeySummary, fetchDailyModelCost, fetchRequestAmount, fetchSummary]);

  useEffect(() => {
    void load();
  }, [load]);

  const requestCurvePoints: RequestAmountPointView[] = requestAmount.points.length > 0
    ? requestAmount.points.map((point) => ({
      hour: point.hour,
      requests: point.requests,
      amount: point.amount,
    }))
    : Array.from({ length: 24 }, (_, hour) => ({
      hour,
      requests: 0,
      amount: 0,
    }));
  const rangeValue = requestAmount.range === "today" ? "今天" : requestAmount.range;

  return (
    <div className="h-full min-h-0 flex flex-col gap-2 p-1">
      <HomeHeader
        title={authKeyMode ? "API Key 概览" : "系统概览"}
        onRefresh={() => void load()}
      />

      <div className="flex-1 min-h-0 overflow-y-auto">
        {loading ? (
          <div className="flex h-full items-center justify-center">
            <Loading message={authKeyMode ? "加载 API Key 概览" : "加载系统概览"} />
          </div>
        ) : authKeyMode ? (
          <AuthKeyDashboard summary={authKeySummary} errorMessage={authKeyError} />
        ) : (
          <div className="space-y-3">
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-3">
              <SummaryCard
                title="请求统计"
                icon={Activity}
                items={[
                  {
                    label: "请求次数",
                    value: <AnimatedCounter value={summary.totalReqs} className="text-base" />,
                    icon: MessageSquare,
                  },
                  {
                    label: "今日请求",
                    value: <AnimatedCounter value={summary.todayReqs} className="text-base" />,
                    icon: Clock,
                  },
                ]}
              />

              <SummaryCard
                title="金额统计"
                icon={Coins}
                items={[
                  {
                    label: "今日消耗金额",
                    value: <span className="text-base font-semibold">{formatMoney(summary.todayAmount)}</span>,
                    icon: CalendarDays,
                  },
                  {
                    label: "总金额",
                    value: <span className="text-base font-semibold">{formatMoney(summary.totalAmount)}</span>,
                    icon: Coins,
                  },
                ]}
              />

              <SummaryCard
                title="令牌统计"
                icon={ArrowDownToLine}
                items={[
                  {
                    label: "今日消耗 Tokens",
                    value: <AnimatedCounter value={summary.todayTokens} className="text-base" />,
                    icon: CalendarDays,
                  },
                  {
                    label: "总消耗 Tokens",
                    value: <AnimatedCounter value={summary.totalTokens} className="text-base" />,
                    icon: ArrowUpToLine,
                  },
                ]}
              />

              <SummaryCard
                title="今日统计"
                icon={CalendarDays}
                items={[
                  {
                    label: "今日成功率",
                    value: <span className="text-base font-semibold">{summary.todaySuccessRate.toFixed(2)}%</span>,
                    icon: BadgeCheck,
                  },
                  {
                    label: "今日成功",
                    value: <AnimatedCounter value={summary.todaySuccessReqs} className="text-base" />,
                    subLabel: `失败 ${summary.todayFailureReqs.toLocaleString()}`,
                    icon: ArrowUpToLine,
                  },
                ]}
              />
            </div>

            <div className="grid grid-cols-1 xl:grid-cols-2 gap-4 xl:items-stretch">
              <DailyModelCostCard trend={dailyModelCost} />

              <TodayAmountTrendCard
                totalRequests={requestAmount.total_requests}
                totalAmount={requestAmount.total_amount}
                rangeLabel="时间范围"
                rangeValue={rangeValue}
                curvePoints={requestCurvePoints}
              />
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
