import { useState, useEffect, useCallback } from 'react';
import { zodResolver } from '@hookform/resolvers/zod';
import { useForm, type FieldErrors } from 'react-hook-form';
import { z } from 'zod';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form';
import Loading from '@/components/loading';
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card';
import { Switch } from '@/components/ui/switch';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import {
  configAPI,
  testProviderProxy,
  type NetworkForwardingConfig,
  type TelegramBreakerAlertConfig,
  type TelegramAgentConfig,
  type ProviderModel,
  type ModelPriceSyncConfig,
  type SystemLogCleanupConfig,
  type GitHubVersionCheckConfig,
  type LoadingUIConfig,
  type ProviderProxyTargetSampleResult,
  type ProviderProxyTargetTestResult,
  type ProviderProxyTestResult,
} from '@/lib/api';
import { toast } from 'sonner';
import { Settings, Network, Coins, FileClock, Type, Send, Github, Sparkles, Bot, Maximize2, Eye, EyeOff, RefreshCw, Gauge, Loader2, Globe2, WifiOff } from 'lucide-react';
import {
  applyLoadingUIStyleSetting,
  loadingUIValues,
  resolveLoadingUIStyle,
  type LoadingUIStyle,
} from '@/lib/loading-ui';

const networkForwardingSchema = z.object({
  telegram_proxy_url: z.string().trim(),
  proxy_ip_enabled: z.boolean(),
  proxy_ip: z.string().trim(),
}).refine((data) => data.telegram_proxy_url.length === 0 || isValidURL(data.telegram_proxy_url), {
  message: 'TG 代理 URL 格式不正确',
  path: ['telegram_proxy_url'],
}).refine((data) => !data.proxy_ip_enabled || data.proxy_ip.length > 0, {
  message: '启用代理 IP 时必须填写代理 IP',
  path: ['proxy_ip'],
});

const priceSyncSchema = z.object({
  enabled: z.boolean(),
  interval_minutes: z.number().min(1, { message: '执行间隔必须大于 0' }),
  source_url: z.string().trim(),
});

const telegramBreakerAlertSchema = z.object({
  enabled: z.boolean(),
  bot_token: z.string().trim(),
  chat_id: z.string().trim(),
  api_base: z.string().trim(),
  status_image_url: z.string().trim(),
}).refine((data) => !data.enabled || data.bot_token.length > 0, {
  message: '启用 TG 告警时必须填写 Bot Token',
  path: ['bot_token'],
}).refine((data) => !data.enabled || data.chat_id.length > 0, {
  message: '启用 TG 告警时必须填写 Chat ID',
  path: ['chat_id'],
}).refine((data) => data.api_base.length === 0 || isValidURL(data.api_base), {
  message: 'TG API 地址格式不正确',
  path: ['api_base'],
}).refine((data) => data.status_image_url.length === 0 || isValidURL(data.status_image_url), {
  message: '状态图片 URL 格式不正确',
  path: ['status_image_url'],
});

const telegramAgentSchema = z.object({
  enabled: z.boolean(),
  skills_enabled: z.boolean(),
  base_url: z.string().trim(),
  api_key: z.string().trim(),
  model: z.string().trim(),
  system_prompt: z.string().trim(),
  max_history_messages: z.number().min(1, { message: '上下文消息数必须大于 0' }),
  max_tokens: z.number().min(1, { message: '最大输出 Tokens 必须大于 0' }),
  edit_interval_ms: z.number().min(300, { message: '编辑间隔不能低于 300ms' }),
}).refine((data) => !data.enabled || data.base_url.length > 0, {
  message: '启用 Agent 时必须填写请求 URL',
  path: ['base_url'],
}).refine((data) => data.base_url.length === 0 || isValidURL(data.base_url), {
  message: '请求 URL 格式不正确',
  path: ['base_url'],
}).refine((data) => !data.enabled || data.api_key.length > 0, {
  message: '启用 Agent 时必须填写 API Key',
  path: ['api_key'],
}).refine((data) => !data.enabled || data.model.length > 0, {
  message: '启用 Agent 时必须填写模型名',
  path: ['model'],
});

const systemLogCleanupSchema = z.object({
  enabled: z.boolean(),
  interval_minutes: z.number().min(1, { message: '清理间隔必须大于 0' }),
});

const uiFontSchema = z.object({
  font: z.enum(['default', 'kunming_seagull', 'fenyuan', 'lxgw_wenkai']),
});

const githubVersionCheckSchema = z.object({
  enabled: z.boolean(),
});

const loadingUISchema = z.object({
  style: z.enum(loadingUIValues),
});

type NetworkForwardingForm = z.infer<typeof networkForwardingSchema>;
type TelegramBreakerAlertForm = z.infer<typeof telegramBreakerAlertSchema>;
type TelegramAgentForm = z.infer<typeof telegramAgentSchema>;
type PriceSyncForm = z.infer<typeof priceSyncSchema>;
type SystemLogCleanupForm = z.infer<typeof systemLogCleanupSchema>;
type UIFontForm = z.infer<typeof uiFontSchema>;
type GitHubVersionCheckForm = z.infer<typeof githubVersionCheckSchema>;
type LoadingUIForm = z.infer<typeof loadingUISchema>;

const UI_FONT_STORAGE_KEY = "orvion_ui_font";
const TELEGRAM_AGENT_CONFIG_CHANGED_EVENT = "telegram-agent-config-changed";

const telegramAgentDefaultValues: TelegramAgentForm = {
  enabled: true,
  skills_enabled: false,
  base_url: '',
  api_key: '',
  model: '',
  system_prompt: '你是 Orvion 的 Telegram 对话助手。请用简体中文回答，保持简洁、准确、友好。',
  max_history_messages: 20,
  max_tokens: 2048,
  edit_interval_ms: 1200,
};

const resolveUIFont = (font?: string): UIFontForm['font'] => {
  if (font === 'kunming_seagull' || font === 'fenyuan' || font === 'lxgw_wenkai') {
    return font;
  }
  return 'default';
};

const loadingUIOptions: { value: LoadingUIStyle; label: string; description: string }[] = [
  { value: 'line_pulse', label: '线条脉冲', description: '细线条有节奏起伏，简洁耐看' },
  { value: 'orbit_ring', label: '环形轨道', description: '单点环绕旋转，科技感更克制' },
  { value: 'slim_progress', label: '细条流动', description: '细进度条来回流动，低干扰' },
  { value: 'ripple_focus', label: '涟漪聚焦', description: '中心扩散涟漪，适合深色浅色主题' },
];

const proxyTargetIconURL: Record<string, string> = {
  bytedance: 'https://ip.net.coffee/favicons/bytedance.webp',
  taobao: 'https://ip.net.coffee/favicons/taobao.webp',
  wechat: 'https://ip.net.coffee/favicons/weixin.webp',
  github: 'https://ip.net.coffee/favicons/github.webp',
  cloudflare: 'https://ip.net.coffee/favicons/cloudflare.webp',
  youtube: 'https://ip.net.coffee/favicons/youtube.webp',
};

const proxyLatencyClass = (target: ProviderProxyTargetTestResult) => {
  if (!target.completed) return 'text-muted-foreground';
  if (!target.ok) return 'text-rose-500';
  const latency = target.latency_ms ?? 0;
  if (latency <= 100) return 'text-emerald-600 dark:text-emerald-300';
  if (latency <= 300) return 'text-lime-500 dark:text-lime-300';
  if (latency <= 800) return 'text-amber-500 dark:text-amber-300';
  return 'text-rose-500';
};

const proxyDotClass = (target: ProviderProxyTargetTestResult, index: number) => {
  const sample = target.samples?.find((item) => item.index === index);
  if (!sample) return 'bg-muted';
  if (!sample.ok) return 'bg-rose-400';
  const latency = sample.latency_ms ?? 9999;
  if (latency <= 300) return 'bg-emerald-500';
  if (latency <= 800) return 'bg-amber-400';
  return 'bg-rose-400';
};

const formatProxyLatency = (target: ProviderProxyTargetTestResult) => (
  target.ok && typeof target.latency_ms === 'number'
    ? `${target.latency_ms}ms`
    : target.completed >= target.total && target.total > 0
      ? '失败'
      : '测速中'
);

const proxyCountryFlag = (countryCode?: string): string => {
  const code = countryCode?.trim().toUpperCase();
  if (!code || !/^[A-Z]{2}$/.test(code)) return '';
  return [...code].map((char) => String.fromCodePoint(127397 + char.charCodeAt(0))).join('');
};

const summarizeProxyTarget = (target: ProviderProxyTargetTestResult): ProviderProxyTargetTestResult => {
  const samples = [...(target.samples ?? [])].sort((a, b) => a.index - b.index);
  const successSamples = samples.filter((sample) => sample.ok && typeof sample.latency_ms === 'number');
  const latencyTotal = successSamples.reduce((total, sample) => total + (sample.latency_ms ?? 0), 0);
  const lastError = [...samples].reverse().find((sample) => sample.error)?.error;
  return {
    ...target,
    samples,
    completed: samples.length,
    total: target.total || 12,
    success_count: successSamples.length,
    ok: successSamples.length > 0,
    latency_ms: successSamples.length > 0 ? Math.round(latencyTotal / successSamples.length) : undefined,
    error: successSamples.length > 0 ? undefined : lastError,
  };
};

const applyProxyTargetSample = (
  target: ProviderProxyTargetTestResult,
  sample: ProviderProxyTargetSampleResult,
): ProviderProxyTargetTestResult => {
  const samples = [...(target.samples ?? []).filter((item) => item.index !== sample.index), sample];
  return summarizeProxyTarget({ ...target, samples });
};

function ProxyTargetIcon({ target }: { target: ProviderProxyTargetTestResult }) {
  const iconURL = proxyTargetIconURL[target.key];
  if (!iconURL) {
    return <Globe2 className="size-4 shrink-0 text-sky-500" />;
  }
  return (
    <img
      src={iconURL}
      alt=""
      className="size-4 shrink-0 rounded-sm object-contain"
      loading="lazy"
      referrerPolicy="no-referrer"
    />
  );
}

function ProxyCountryDisplay({ result, loading }: { result: ProviderProxyTestResult | null; loading: boolean }) {
  const flag = proxyCountryFlag(result?.exit_country_code);
  const text = [
    result?.exit_country,
    result?.exit_country_code ? `(${result.exit_country_code})` : '',
  ].filter(Boolean).join(' ') || (loading ? '检测中...' : '未知');

  return (
    <div className="mt-1 flex min-w-0 items-center gap-1.5 font-medium">
      <span className="truncate">{text}</span>
      {flag && (
        <span className="shrink-0 text-sm leading-none" aria-label={`${result?.exit_country_code} 国旗`}>
          {flag}
        </span>
      )}
    </div>
  );
}

const isValidURL = (raw: string): boolean => {
  try {
    const parsed = new URL(raw);
    return parsed.protocol.length > 0;
  } catch {
    return false;
  }
};

const applyUIFontSetting = (font: UIFontForm["font"]) => {
  if (typeof window !== "undefined") {
    window.localStorage.setItem(UI_FONT_STORAGE_KEY, font);
    window.dispatchEvent(new CustomEvent("ui-font-changed", { detail: { font } }));
  }
  if (typeof document !== "undefined") {
    document.documentElement.dataset.uiFont = font;
  }
};

export default function ConfigPage() {
  const [loading, setLoading] = useState(true);
  const [priceSyncing, setPriceSyncing] = useState(false);
  const [tgTesting, setTgTesting] = useState(false);
  const [telegramAgentSaving, setTelegramAgentSaving] = useState(false);
  const [telegramAgentPromptDialogOpen, setTelegramAgentPromptDialogOpen] = useState(false);
  const [telegramAgentModelDialogOpen, setTelegramAgentModelDialogOpen] = useState(false);
  const [telegramAgentAPIKeyVisible, setTelegramAgentAPIKeyVisible] = useState(false);
  const [telegramAgentModelsLoading, setTelegramAgentModelsLoading] = useState(false);
  const [telegramAgentModelOptions, setTelegramAgentModelOptions] = useState<ProviderModel[]>([]);
  const [telegramAgentModelDropdownOpen, setTelegramAgentModelDropdownOpen] = useState(false);
  const [displayConfigSaving, setDisplayConfigSaving] = useState(false);
  const [coreConfigSaving, setCoreConfigSaving] = useState(false);
  const [networkForwardingSaving, setNetworkForwardingSaving] = useState(false);
  const [proxyTestOpen, setProxyTestOpen] = useState(false);
  const [proxyTestLoading, setProxyTestLoading] = useState(false);
  const [proxyTestResult, setProxyTestResult] = useState<ProviderProxyTestResult | null>(null);
  const [proxyTestError, setProxyTestError] = useState<string | null>(null);
  const [proxyTestProxyURL, setProxyTestProxyURL] = useState('');
  const networkForwardingForm = useForm<NetworkForwardingForm>({
    resolver: zodResolver(networkForwardingSchema),
    defaultValues: {
      telegram_proxy_url: '',
      proxy_ip_enabled: false,
      proxy_ip: '',
    },
  });

  const priceSyncForm = useForm<PriceSyncForm>({
    resolver: zodResolver(priceSyncSchema),
    defaultValues: {
      enabled: true,
      interval_minutes: 1440,
      source_url: 'https://models.dev/api.json',
    },
  });

  const telegramBreakerAlertForm = useForm<TelegramBreakerAlertForm>({
    resolver: zodResolver(telegramBreakerAlertSchema),
    defaultValues: {
      enabled: false,
      bot_token: '',
      chat_id: '',
      api_base: 'https://api.telegram.org',
      status_image_url: 'https://i.mukyu.ru/random?wtf_gender=girls',
    },
  });

  const telegramAgentForm = useForm<TelegramAgentForm>({
    resolver: zodResolver(telegramAgentSchema),
    defaultValues: telegramAgentDefaultValues,
  });

  const systemLogCleanupForm = useForm<SystemLogCleanupForm>({
    resolver: zodResolver(systemLogCleanupSchema),
    defaultValues: {
      enabled: true,
      interval_minutes: 1440,
    },
  });

  const uiFontForm = useForm<UIFontForm>({
    resolver: zodResolver(uiFontSchema),
    defaultValues: {
      font: 'default',
    },
  });

  const githubVersionCheckForm = useForm<GitHubVersionCheckForm>({
    resolver: zodResolver(githubVersionCheckSchema),
    defaultValues: {
      enabled: true,
    },
  });

  const loadingUIForm = useForm<LoadingUIForm>({
    resolver: zodResolver(loadingUISchema),
    defaultValues: {
      style: 'line_pulse',
    },
  });

  const fetchConfig = useCallback(async () => {
    try {
      setLoading(true);
      const networkForwardingResponse = await configAPI.getConfig('network_forwarding');
      if (networkForwardingResponse.value) {
        const networkCfg = JSON.parse(networkForwardingResponse.value) as NetworkForwardingConfig;
        const nextNetworkForwardingConfig = {
          telegram_proxy_url: networkCfg.telegram_proxy_url || '',
          proxy_ip_enabled: Boolean(networkCfg.proxy_ip_enabled),
          proxy_ip: networkCfg.proxy_ip || '',
        };
        networkForwardingForm.reset(nextNetworkForwardingConfig);
      }
    } catch (error) {
      console.error('Failed to load network forwarding config:', error);
    }

    try {
      const telegramResponse = await configAPI.getConfig('breaker_alert_tg');
      if (telegramResponse.value) {
        const tgCfg = JSON.parse(telegramResponse.value) as TelegramBreakerAlertConfig;
        const nextTGConfig = {
          enabled: Boolean(tgCfg.enabled),
          bot_token: tgCfg.bot_token || '',
          chat_id: tgCfg.chat_id || '',
          api_base: tgCfg.api_base || 'https://api.telegram.org',
          status_image_url: tgCfg.status_image_url || 'https://i.mukyu.ru/random?wtf_gender=girls',
        };
        telegramBreakerAlertForm.reset(nextTGConfig);
      }
    } catch (error) {
      console.error('Failed to load telegram breaker alert config:', error);
    }

    try {
      const telegramAgentResponse = await configAPI.getConfig('telegram_agent');
      if (telegramAgentResponse.value) {
        const agentCfg = JSON.parse(telegramAgentResponse.value) as TelegramAgentConfig;
        const nextTelegramAgentConfig = {
          enabled: agentCfg.enabled !== false,
          skills_enabled: agentCfg.skills_enabled === true,
          base_url: agentCfg.base_url || '',
          api_key: agentCfg.api_key || '',
          model: agentCfg.model || '',
          system_prompt: agentCfg.system_prompt || telegramAgentDefaultValues.system_prompt,
          max_history_messages: Number(agentCfg.max_history_messages || telegramAgentDefaultValues.max_history_messages),
          max_tokens: Number(agentCfg.max_tokens || telegramAgentDefaultValues.max_tokens),
          edit_interval_ms: Number(agentCfg.edit_interval_ms || telegramAgentDefaultValues.edit_interval_ms),
        };
        telegramAgentForm.reset(nextTelegramAgentConfig);
      } else {
        telegramAgentForm.reset(telegramAgentDefaultValues);
      }
    } catch (error) {
      console.error('Failed to load telegram agent config:', error);
    }

    try {
      // 获取模型价格同步配置
      const priceSyncResponse = await configAPI.getConfig('model_price_sync');
      if (priceSyncResponse.value) {
        const priceSyncCfg = JSON.parse(priceSyncResponse.value) as ModelPriceSyncConfig;
        const nextPriceSyncConfig = {
          enabled: Boolean(priceSyncCfg.enabled),
          interval_minutes: Number(priceSyncCfg.interval_minutes || 1440),
          source_url: priceSyncCfg.source_url || 'https://models.dev/api.json',
        };
        priceSyncForm.reset(nextPriceSyncConfig);
      }
    } catch (error) {
      console.error('Failed to load model price sync config:', error);
    }

    try {
      const systemLogCleanupResponse = await configAPI.getConfig('system_log_cleanup');
      if (systemLogCleanupResponse.value) {
        const cleanupCfg = JSON.parse(systemLogCleanupResponse.value) as SystemLogCleanupConfig;
        const nextCleanupConfig = {
          enabled: Boolean(cleanupCfg.enabled),
          interval_minutes: Number(cleanupCfg.interval_minutes || 1440),
        };
        systemLogCleanupForm.reset(nextCleanupConfig);
      }
    } catch (error) {
      console.error('Failed to load system log cleanup config:', error);
    }

    try {
      const uiFontResponse = await configAPI.getConfig('ui_font');
      if (uiFontResponse.value) {
        const fontCfg = JSON.parse(uiFontResponse.value) as { font?: string };
        const nextFont = resolveUIFont(fontCfg.font);
        uiFontForm.reset({ font: nextFont });
        applyUIFontSetting(nextFont);
      }
    } catch (error) {
      console.error('Failed to load ui font config:', error);
    }

    try {
      const githubVersionCheckResponse = await configAPI.getConfig('github_version_check');
      if (githubVersionCheckResponse.value) {
        const githubVersionCheckCfg = JSON.parse(githubVersionCheckResponse.value) as GitHubVersionCheckConfig;
        githubVersionCheckForm.reset({
          enabled: githubVersionCheckCfg.enabled !== false,
        });
      }
    } catch (error) {
      console.error('Failed to load github version check config:', error);
    }

    try {
      const loadingUIResponse = await configAPI.getConfig('ui_loading_style');
      if (loadingUIResponse.value) {
        const loadingUICfg = JSON.parse(loadingUIResponse.value) as LoadingUIConfig;
        const nextStyle = resolveLoadingUIStyle(loadingUICfg.style);
        loadingUIForm.reset({ style: nextStyle });
        applyLoadingUIStyleSetting(nextStyle);
      }
    } catch (error) {
      console.error('Failed to load loading ui config:', error);
    }

    setLoading(false);
  }, [githubVersionCheckForm, loadingUIForm, networkForwardingForm, priceSyncForm, systemLogCleanupForm, telegramAgentForm, telegramBreakerAlertForm, uiFontForm]);

  useEffect(() => {
    void fetchConfig();
  }, [fetchConfig]);

  const onTelegramBreakerAlertSubmit = async (values: TelegramBreakerAlertForm) => {
    try {
      await configAPI.updateConfig('breaker_alert_tg', values);
      toast.success('TG 告警配置已保存');
    } catch (error) {
      console.error('Failed to save telegram breaker alert config:', error);
      toast.error('保存 TG 告警配置失败');
    }
  };

  const handleRunTelegramBreakerAlertTest = async () => {
    try {
      setTgTesting(true);
      await configAPI.runTelegramBreakerAlertTest();
      toast.success('TG 测试消息已发送');
    } catch (error) {
      console.error('Failed to run telegram breaker alert test:', error);
      const message = error instanceof Error ? error.message : '发送 TG 测试消息失败';
      toast.error(message);
    } finally {
      setTgTesting(false);
    }
  };

  const onNetworkForwardingSubmit = async (values: NetworkForwardingForm) => {
    try {
      setNetworkForwardingSaving(true);
      await configAPI.updateConfig('network_forwarding', values);
      toast.success('网络转发配置已保存');
    } catch (error) {
      console.error('Failed to save network forwarding config:', error);
      toast.error('保存网络转发配置失败');
    } finally {
      setNetworkForwardingSaving(false);
    }
  };

  const handleNetworkProxyTest = async () => {
    const proxyURL = networkForwardingForm.getValues('telegram_proxy_url').trim();
    if (!proxyURL) {
      networkForwardingForm.setError('telegram_proxy_url', { message: '请先填写 TG 代理 URL' });
      return;
    }
    if (!isValidURL(proxyURL)) {
      networkForwardingForm.setError('telegram_proxy_url', { message: 'TG 代理 URL 格式不正确' });
      return;
    }

    networkForwardingForm.clearErrors('telegram_proxy_url');
    setProxyTestProxyURL(proxyURL);
    setProxyTestResult(null);
    setProxyTestError(null);
    setProxyTestLoading(true);
    setProxyTestOpen(true);
    try {
      await testProviderProxy(proxyURL, (event) => {
        if (event.type === 'init') {
          setProxyTestResult({
            targets: (event.targets ?? []).map((target) => summarizeProxyTarget(target)),
          });
          return;
        }
        if (event.type === 'exit') {
          setProxyTestResult((current) => ({
            ...(current ?? { targets: [] }),
            exit_ip: event.exit_ip,
            exit_country: event.exit_country,
            exit_country_code: event.exit_country_code,
            exit_error: event.exit_error,
          }));
          return;
        }
        if (event.type === 'target_sample' && event.target_key && event.sample) {
          setProxyTestResult((current) => {
            if (!current) return current;
            return {
              ...current,
              targets: current.targets.map((target) => (
                target.key === event.target_key ? applyProxyTargetSample(target, event.sample!) : target
              )),
            };
          });
          return;
        }
        if (event.type === 'target_done' && event.target_key && event.target) {
          setProxyTestResult((current) => {
            if (!current) return current;
            return {
              ...current,
              targets: current.targets.map((target) => (
                target.key === event.target_key ? summarizeProxyTarget(event.target!) : target
              )),
            };
          });
        }
      });
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      setProxyTestError(message);
      toast.error(`代理测速失败: ${message}`);
    } finally {
      setProxyTestLoading(false);
    }
  };

  const onTelegramAgentSubmit = async (values: TelegramAgentForm) => {
    const payload: TelegramAgentConfig = {
      ...values,
      base_url: values.base_url.trim(),
      api_key: values.api_key.trim(),
      model: values.model.trim(),
      system_prompt: values.system_prompt.trim(),
    };

    try {
      setTelegramAgentSaving(true);
      await configAPI.updateConfig('telegram_agent', payload);
      window.dispatchEvent(new CustomEvent(TELEGRAM_AGENT_CONFIG_CHANGED_EVENT, {
        detail: { enabled: payload.enabled !== false },
      }));
      setTelegramAgentModelDialogOpen(false);
      toast.success('TG Agent 配置已保存');
    } catch (error) {
      console.error('Failed to save telegram agent config:', error);
      toast.error('保存 TG Agent 配置失败');
    } finally {
      setTelegramAgentSaving(false);
    }
  };

  const handleFetchTelegramAgentModels = async () => {
    const baseURL = telegramAgentForm.getValues('base_url').trim();
	const apiKey = telegramAgentForm.getValues('api_key').trim();
	if (!baseURL) {
		setTelegramAgentModelDialogOpen(true);
		telegramAgentForm.clearErrors('base_url');
		return;
	}
    if (!isValidURL(baseURL)) {
      setTelegramAgentModelDialogOpen(true);
      telegramAgentForm.setError('base_url', { message: '请求 URL 格式不正确' });
      return;
    }
    if (!apiKey) {
      setTelegramAgentModelDialogOpen(true);
      telegramAgentForm.setError('api_key', { message: '请先填写 API Key' });
      return;
    }

    try {
      setTelegramAgentModelsLoading(true);
      const models = await configAPI.getTelegramAgentModels({ base_url: baseURL, api_key: apiKey });
      const sortedModels = [...models].sort((a, b) => a.id.localeCompare(b.id));
      setTelegramAgentModelOptions(sortedModels);
      setTelegramAgentModelDropdownOpen(sortedModels.length > 0);
      toast.success(`已获取 ${sortedModels.length} 个模型`);
    } catch (error) {
      console.error('Failed to fetch telegram agent models:', error);
      setTelegramAgentModelOptions([]);
      setTelegramAgentModelDropdownOpen(false);
      toast.error(error instanceof Error ? error.message : '获取模型列表失败');
    } finally {
      setTelegramAgentModelsLoading(false);
    }
  };

  const onTelegramAgentSubmitInvalid = (errors: FieldErrors<TelegramAgentForm>) => {
    if (
      errors.base_url ||
      errors.api_key ||
      errors.model ||
      errors.max_history_messages ||
      errors.max_tokens ||
      errors.edit_interval_ms
    ) {
      setTelegramAgentModelDialogOpen(true);
    }
  };

  const onCoreConfigSubmit = async () => {
    const [cleanupValid, priceSyncValid] = await Promise.all([
      systemLogCleanupForm.trigger(),
      priceSyncForm.trigger(),
    ]);
    if (!cleanupValid || !priceSyncValid) {
      return;
    }

    const cleanupValues = systemLogCleanupForm.getValues();
    const priceSyncValues = priceSyncForm.getValues();

    try {
      setCoreConfigSaving(true);
      await Promise.all([
        configAPI.updateConfig('system_log_cleanup', cleanupValues),
        configAPI.updateConfig('model_price_sync', priceSyncValues),
      ]);
      toast.success('核心配置已保存');
    } catch (error) {
      console.error('Failed to save core config:', error);
      toast.error('保存核心配置失败');
    } finally {
      setCoreConfigSaving(false);
    }
  };

  const onDisplayConfigSubmit = async () => {
    const [githubValid, fontValid, loadingUIValid] = await Promise.all([
      githubVersionCheckForm.trigger(),
      uiFontForm.trigger(),
      loadingUIForm.trigger(),
    ]);
    if (!githubValid || !fontValid || !loadingUIValid) {
      return;
    }

    const githubValues = githubVersionCheckForm.getValues();
    const fontValues = uiFontForm.getValues();
    const loadingUIValues = loadingUIForm.getValues();

    try {
      setDisplayConfigSaving(true);
      await Promise.all([
        configAPI.updateConfig('github_version_check', githubValues),
        configAPI.updateConfig('ui_font', fontValues),
        configAPI.updateConfig('ui_loading_style', loadingUIValues),
      ]);
      applyUIFontSetting(fontValues.font);
      applyLoadingUIStyleSetting(loadingUIValues.style);
      toast.success('系统显示配置已保存');
    } catch (error) {
      console.error('Failed to save display config:', error);
      toast.error('保存系统显示配置失败');
    } finally {
      setDisplayConfigSaving(false);
    }
  };

  const handleRunPriceSync = async () => {
    try {
      setPriceSyncing(true);
      await configAPI.runModelPriceSync();
      toast.success('模型价格拉取完成，已同步');
    } catch (error) {
      console.error('Failed to run model price sync:', error);
      toast.error('模型价格同步失败');
    } finally {
      setPriceSyncing(false);
    }
  };

  if (loading) {
    return (
      <div className="flex h-full min-h-0 items-center justify-center">
        <Loading />
      </div>
    );
  }

  return (
    <div className="h-full min-h-0 flex flex-col gap-4 p-1">
      <div className="flex flex-col gap-2 flex-shrink-0">
        <div className="flex flex-wrap items-start justify-between gap-2">
          <div className="min-w-0 flex items-center gap-2">
            <span className="flex size-9 items-center justify-center rounded-2xl bg-emerald-100 text-emerald-700">
              <Settings className="size-4" />
            </span>
            <h2 className="text-2xl font-bold tracking-tight">设置</h2>
          </div>
        </div>
      </div>

      <div className="flex-1 min-h-0 overflow-y-auto">
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-2 xl:grid-cols-3">
          <Card className="rounded-2xl border border-border/60 bg-card/90 shadow-[0_18px_45px_rgba(0,0,0,0.08)] lg:order-1">
            <CardContent className="space-y-3">
              <div className="space-y-3">
                <div className="space-y-1">
                  <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
                    <Github className="size-4 text-emerald-600" />
                    GitHub 更新检查
                  </div>
                  <p className="text-xs text-muted-foreground">
                    控制后台是否定时连接 GitHub 检查新版本
                  </p>
                </div>
                <Form {...githubVersionCheckForm}>
                  <div className="space-y-3">
                    <FormField
                      control={githubVersionCheckForm.control}
                      name="enabled"
                      render={({ field }) => (
                        <FormItem className="flex items-center justify-between gap-3 rounded-lg border border-border/60 bg-muted/50 px-3 py-2">
                          <FormLabel className="text-xs text-muted-foreground">启用更新检查</FormLabel>
                          <FormControl>
                            <Switch
                              checked={field.value === true}
                              onCheckedChange={(checked) => field.onChange(checked === true)}
                            />
                          </FormControl>
                        </FormItem>
                      )}
                    />
                  </div>
                </Form>
              </div>

              <div className="space-y-3 border-t border-border/60 pt-2">
                <div className="space-y-1">
                  <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
                    <Type className="size-4 text-emerald-600" />
                    界面字体配置
                  </div>
                  <p className="text-xs text-muted-foreground">
                    当前支持默认字体、昆明海鸥体、粉圆体和霞鹜文楷
                  </p>
                </div>
                <Form {...uiFontForm}>
                  <div className="space-y-3">
                    <FormField
                      control={uiFontForm.control}
                      name="font"
                      render={({ field }) => (
                        <FormItem className="space-y-1">
                          <div className="flex items-center gap-1.5">
                            <FormLabel className="w-[3.75rem] shrink-0 text-xs text-muted-foreground">字体</FormLabel>
                            <FormControl>
                              <Select value={field.value} onValueChange={(value) => field.onChange(value as UIFontForm['font'])}>
                                <SelectTrigger className="h-9 w-full bg-background">
                                  <SelectValue placeholder="选择字体" />
                                </SelectTrigger>
                                <SelectContent>
                                  <SelectItem value="default">默认字体</SelectItem>
                                  <SelectItem value="kunming_seagull">昆明海鸥体</SelectItem>
                                  <SelectItem value="fenyuan">粉圆体</SelectItem>
                                  <SelectItem value="lxgw_wenkai">霞鹜文楷</SelectItem>
                                </SelectContent>
                              </Select>
                            </FormControl>
                          </div>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                  </div>
                </Form>
              </div>

              <div className="space-y-3 border-t border-border/60 pt-2">
                <div className="space-y-1">
                  <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
                    <Sparkles className="size-4 text-emerald-600" />
                    加载 UI 切换
                  </div>
                  <p className="text-xs text-muted-foreground">
                    选择全局加载动画样式，支持简约风与 GIF 风格
                  </p>
                </div>
                <Form {...loadingUIForm}>
                  <div className="space-y-3">
                    <FormField
                      control={loadingUIForm.control}
                      name="style"
                      render={({ field }) => (
                        <FormItem className="space-y-1">
                          <div className="flex items-center gap-1.5">
                            <FormLabel className="w-[3.75rem] shrink-0 text-xs text-muted-foreground">加载样式</FormLabel>
                            <FormControl>
                              <Select
                                value={field.value}
                                onValueChange={(value) => {
                                  const nextValue = resolveLoadingUIStyle(value);
                                  field.onChange(nextValue);
                                  applyLoadingUIStyleSetting(nextValue);
                                }}
                              >
                                <SelectTrigger className="h-9 w-full bg-background">
                                  <SelectValue placeholder="选择加载样式" />
                                </SelectTrigger>
                                <SelectContent>
                                  {loadingUIOptions.map((option) => (
                                    <SelectItem key={option.value} value={option.value}>
                                      {option.label}
                                    </SelectItem>
                                  ))}
                                </SelectContent>
                              </Select>
                            </FormControl>
                          </div>
                          <p className="text-[11px] text-muted-foreground">
                            {loadingUIOptions.find((option) => option.value === field.value)?.description ?? ''}
                          </p>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                  </div>
                </Form>
              </div>
            </CardContent>
            <CardFooter className="justify-end">
              <Button type="button" onClick={() => void onDisplayConfigSubmit()} disabled={displayConfigSaving}>
                {displayConfigSaving ? '保存中...' : '保存配置'}
              </Button>
            </CardFooter>
          </Card>

          <Card className="gap-1 rounded-2xl border border-border/60 bg-card/90 shadow-[0_18px_45px_rgba(0,0,0,0.08)] lg:order-4 xl:order-5">
            <CardHeader className="pb-2">
              <CardTitle className="flex items-center gap-2 text-sm font-semibold">
                <Network className="size-4 text-emerald-600" />
                网络转发配置
              </CardTitle>
            </CardHeader>
            <CardContent>
              <Form {...networkForwardingForm}>
                <form
                  id="network-forwarding-form"
                  onSubmit={networkForwardingForm.handleSubmit(onNetworkForwardingSubmit)}
                  className="space-y-4"
                >
                  <FormField
                    control={networkForwardingForm.control}
                    name="telegram_proxy_url"
                    render={({ field }) => (
                      <FormItem className="space-y-1">
                        <div className="flex items-center gap-1.5">
                          <FormLabel className="w-[4.5rem] shrink-0 text-xs text-muted-foreground">TG 代理</FormLabel>
                          <FormControl>
                            <div className="flex min-w-0 flex-1 gap-2">
                              <Input className="min-w-0 flex-1" placeholder="http://127.0.0.1:7890 或 socks5://127.0.0.1:1080" {...field} />
                              <Button
                                type="button"
                                variant="outline"
                                size="icon"
                                className="h-9 shrink-0"
                                onClick={() => void handleNetworkProxyTest()}
                                disabled={proxyTestLoading}
                                aria-label="测试代理连通性"
                              >
                                {proxyTestLoading ? <Loader2 className="size-4 animate-spin" /> : <Gauge className="size-4" />}
                              </Button>
                            </div>
                          </FormControl>
                        </div>
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <div className="grid grid-cols-1 items-start gap-3 sm:grid-cols-[8.75rem_minmax(0,1fr)]">
                    <FormField
                      control={networkForwardingForm.control}
                      name="proxy_ip_enabled"
                      render={({ field }) => (
                        <FormItem className="flex h-9 items-center justify-between gap-3 rounded-lg border border-border/60 bg-muted/50 px-3">
                          <FormLabel className="text-xs text-muted-foreground">启用 IP 覆盖</FormLabel>
                          <FormControl>
                            <Switch
                              checked={field.value === true}
                              onCheckedChange={(checked) => field.onChange(checked === true)}
                            />
                          </FormControl>
                        </FormItem>
                      )}
                    />

                    <FormField
                      control={networkForwardingForm.control}
                      name="proxy_ip"
                      render={({ field }) => (
                        <FormItem className="space-y-1">
                          <div className="flex items-center gap-1.5">
                            <FormLabel className="shrink-0 text-xs text-muted-foreground">覆盖 IP</FormLabel>
                            <FormControl>
                              <Input className="h-9" placeholder="203.0.113.10" {...field} />
                            </FormControl>
                          </div>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                  </div>
                </form>
              </Form>
            </CardContent>
            <CardFooter className="justify-end">
              <Button type="submit" form="network-forwarding-form" disabled={networkForwardingSaving}>
                {networkForwardingSaving ? '保存中...' : '保存配置'}
              </Button>
            </CardFooter>
          </Card>

          <Card className="gap-1 rounded-2xl border border-border/60 bg-card/90 shadow-[0_18px_45px_rgba(0,0,0,0.08)] lg:order-2">
            <CardHeader className="pb-2">
              <CardTitle className="flex items-center gap-2 text-sm font-semibold">
                <Send className="size-4 text-emerald-600" />
                TG 告警配置
              </CardTitle>
              <CardDescription className="text-xs">
                配置熔断告警发送到 Telegram 的 Bot 参数、API 地址与状态图片。
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <Form {...telegramBreakerAlertForm}>
                <form id="telegram-breaker-alert-form" onSubmit={telegramBreakerAlertForm.handleSubmit(onTelegramBreakerAlertSubmit)} className="space-y-4">
                  <FormField
                    control={telegramBreakerAlertForm.control}
                    name="enabled"
                    render={({ field }) => (
                      <FormItem className="flex items-center justify-between gap-3 rounded-lg border border-border/60 bg-muted/50 px-3 py-2">
                        <FormLabel className="text-xs text-muted-foreground">启用 TG 告警</FormLabel>
                        <FormControl>
                          <Switch
                            checked={field.value === true}
                            onCheckedChange={(checked) => field.onChange(checked === true)}
                          />
                        </FormControl>
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={telegramBreakerAlertForm.control}
                    name="bot_token"
                    render={({ field }) => (
                      <FormItem className="space-y-1">
                        <div className="flex items-center gap-1.5">
                          <FormLabel className="w-[3.75rem] shrink-0 text-xs text-muted-foreground">Bot Token</FormLabel>
                          <FormControl>
                            <Input type="password" placeholder="123456789:AA..." {...field} />
                          </FormControl>
                        </div>
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={telegramBreakerAlertForm.control}
                    name="chat_id"
                    render={({ field }) => (
                      <FormItem className="space-y-1">
                        <div className="flex items-center gap-1.5">
                          <FormLabel className="w-[3.75rem] shrink-0 text-xs text-muted-foreground">Chat ID</FormLabel>
                          <FormControl>
                            <Input placeholder="-1001234567890" {...field} />
                          </FormControl>
                        </div>
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <div className="space-y-3">
                      <FormField
                        control={telegramBreakerAlertForm.control}
                        name="api_base"
                        render={({ field }) => (
                          <FormItem className="space-y-1">
                            <div className="flex items-center gap-1.5">
                              <FormLabel className="w-[3.75rem] shrink-0 text-xs text-muted-foreground">TG API</FormLabel>
                              <FormControl>
                                <Input placeholder="https://api.telegram.org" {...field} />
                              </FormControl>
                            </div>
                            <FormMessage />
                          </FormItem>
                        )}
                      />

                      <FormField
                        control={telegramBreakerAlertForm.control}
                        name="status_image_url"
                        render={({ field }) => (
                          <FormItem className="space-y-1">
                            <div className="flex items-center gap-1.5">
                              <FormLabel className="w-[3.75rem] shrink-0 text-xs text-muted-foreground">图片 URL</FormLabel>
                              <FormControl>
                                <Input placeholder="https://i.mukyu.ru/random?wtf_gender=girls" {...field} />
                              </FormControl>
                            </div>
                            <FormMessage />
                          </FormItem>
                        )}
                      />
                  </div>
                </form>
              </Form>
            </CardContent>
            <CardFooter className="flex justify-between">
              <Button type="button" variant="outline" onClick={handleRunTelegramBreakerAlertTest} disabled={tgTesting}>
                {tgTesting ? '发送中...' : '发送测试消息'}
              </Button>
              <Button type="submit" form="telegram-breaker-alert-form">保存配置</Button>
            </CardFooter>
          </Card>

          <Card className="gap-1 rounded-2xl border border-border/60 bg-card/90 shadow-[0_18px_45px_rgba(0,0,0,0.08)] lg:order-5 xl:order-3">
            <CardHeader className="pb-2">
              <CardTitle className="flex items-center gap-2 text-sm font-semibold">
                <Bot className="size-4 text-emerald-600" />
                TG Agent 配置
              </CardTitle>
              <CardDescription className="text-xs">
                配置 Telegram 对话助手的开关、工具能力与系统提示词。
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <Form {...telegramAgentForm}>
                <form id="telegram-agent-form" onSubmit={telegramAgentForm.handleSubmit(onTelegramAgentSubmit, onTelegramAgentSubmitInvalid)} className="space-y-4">
                  <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                    <FormField
                      control={telegramAgentForm.control}
                      name="enabled"
                      render={({ field }) => (
                        <FormItem className="flex h-9 items-center justify-between gap-3 rounded-lg border border-border/60 bg-muted/50 px-3">
                          <FormLabel className="text-xs text-muted-foreground">启用对话</FormLabel>
                          <FormControl>
                            <Switch
                              checked={field.value === true}
                              onCheckedChange={(checked) => field.onChange(checked === true)}
                            />
                          </FormControl>
                        </FormItem>
                      )}
                    />
                    <FormField
                      control={telegramAgentForm.control}
                      name="skills_enabled"
                      render={({ field }) => (
                        <FormItem className="flex h-9 items-center justify-between gap-3 rounded-lg border border-border/60 bg-muted/50 px-3">
                          <FormLabel className="text-xs text-muted-foreground">启用 Skills</FormLabel>
                          <FormControl>
                            <Switch
                              checked={field.value === true}
                              onCheckedChange={(checked) => field.onChange(checked === true)}
                            />
                          </FormControl>
                        </FormItem>
                      )}
                    />
                  </div>

                  <Button
                    type="button"
                    variant="outline"
                    className="h-9 w-full justify-start gap-2 text-sm sm:w-auto"
                    onClick={() => setTelegramAgentModelDialogOpen(true)}
                  >
                    <Settings className="size-4 text-emerald-600" />
                    Agent 模型配置
                  </Button>

                  <FormField
                    control={telegramAgentForm.control}
                    name="system_prompt"
                    render={({ field }) => (
                      <FormItem className="space-y-1">
                        <div className="flex items-center justify-between gap-2">
                          <FormLabel className="text-xs text-muted-foreground">系统提示词</FormLabel>
                          <Button
                            type="button"
                            variant="outline"
                            className="h-7 gap-1.5 px-2 text-xs"
                            onClick={() => setTelegramAgentPromptDialogOpen(true)}
                          >
                            <Maximize2 className="size-3.5" />
                            展开
                          </Button>
                        </div>
                        <FormControl>
                          <Textarea className="h-[76px] min-h-0 resize-none overflow-hidden text-sm leading-5" rows={3} {...field} />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                </form>
              </Form>
            </CardContent>
            <CardFooter className="justify-end">
              <Button type="submit" form="telegram-agent-form" disabled={telegramAgentSaving}>
                {telegramAgentSaving ? '保存中...' : '保存配置'}
              </Button>
            </CardFooter>
          </Card>

          <Card className="rounded-2xl border border-border/60 bg-card/90 shadow-[0_18px_45px_rgba(0,0,0,0.08)] lg:order-3 xl:order-4">
            <CardContent className="grid grid-cols-1 gap-4 xl:grid-cols-2">
              <div className="space-y-2">
                <div className="space-y-0.5">
                  <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
                    <FileClock className="size-4 text-emerald-600" />
                    系统日志自动清理
                  </div>
                </div>
                <Form {...systemLogCleanupForm}>
                  <div className="space-y-3">
                    <FormField
                      control={systemLogCleanupForm.control}
                      name="enabled"
                      render={({ field }) => (
                        <FormItem className="flex h-9 items-center justify-between gap-3 rounded-lg border border-border/60 bg-muted/50 px-3">
                          <FormLabel className="text-xs text-muted-foreground">启用自动清理</FormLabel>
                          <FormControl>
                            <Switch
                              checked={field.value === true}
                              onCheckedChange={(checked) => field.onChange(checked === true)}
                            />
                          </FormControl>
                        </FormItem>
                      )}
                    />

                    <FormField
                      control={systemLogCleanupForm.control}
                      name="interval_minutes"
                      render={({ field }) => (
                        <FormItem className="space-y-0">
                          <FormLabel className="sr-only">清理间隔（分钟）</FormLabel>
                          <FormControl>
                            <div className="relative">
                              <Input
                                className="h-9 pr-10"
                                type="number"
                                min={1}
                                placeholder="清理间隔"
                                value={field.value}
                                onChange={(event) => field.onChange(Number(event.target.value))}
                              />
                              <span className="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-xs text-muted-foreground">
                                分钟
                              </span>
                            </div>
                          </FormControl>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                  </div>
                </Form>
              </div>

              <div className="space-y-2 border-t border-border/60 pt-3 xl:border-l xl:border-t-0 xl:pl-4 xl:pt-0">
                <div className="space-y-0.5">
                  <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
                    <Coins className="size-4 text-emerald-600" />
                    模型价格同步配置
                  </div>
                </div>
                <Form {...priceSyncForm}>
                  <div className="space-y-3">
                    <div className="space-y-3">
                      <FormField
                        control={priceSyncForm.control}
                        name="enabled"
                        render={({ field }) => (
                          <FormItem className="flex h-9 items-center justify-between gap-3 rounded-lg border border-border/60 bg-muted/50 px-3">
                            <FormLabel className="text-xs text-muted-foreground">启用同步</FormLabel>
                            <FormControl>
                              <Switch
                                checked={field.value === true}
                                onCheckedChange={(checked) => field.onChange(checked === true)}
                              />
                            </FormControl>
                          </FormItem>
                        )}
                      />

                      <FormField
                        control={priceSyncForm.control}
                        name="interval_minutes"
                        render={({ field }) => (
                          <FormItem className="space-y-0">
                            <FormLabel className="sr-only">执行间隔（分钟）</FormLabel>
                            <FormControl>
                              <div className="relative">
                                <Input
                                  className="h-9 pr-10"
                                  type="number"
                                  min={1}
                                  placeholder="执行间隔"
                                  value={field.value}
                                  onChange={(event) => field.onChange(Number(event.target.value))}
                                />
                                <span className="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-xs text-muted-foreground">
                                  分钟
                                </span>
                              </div>
                            </FormControl>
                            <FormMessage />
                          </FormItem>
                        )}
                      />
                    </div>

                  </div>
                </Form>
              </div>

            </CardContent>
            <CardFooter className="flex justify-between">
              <Button type="button" variant="outline" onClick={handleRunPriceSync} disabled={priceSyncing}>
                {priceSyncing ? '正在拉取...' : '立刻拉取'}
              </Button>
              <Button type="button" onClick={() => void onCoreConfigSubmit()} disabled={coreConfigSaving}>
                {coreConfigSaving ? '保存中...' : '保存配置'}
              </Button>
            </CardFooter>
          </Card>

        </div>
      </div>

      <Dialog open={proxyTestOpen} onOpenChange={setProxyTestOpen}>
        <DialogContent className="max-w-5xl gap-5">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <Gauge className="size-5 text-emerald-500" />
              网络连通性
            </DialogTitle>
            <DialogDescription>
              使用网络转发配置中的 TG 代理测试出口信息与常用站点延迟
            </DialogDescription>
          </DialogHeader>

          <div className="rounded-lg border border-border/70 bg-muted/30 px-4 py-3">
            <div className="grid gap-3 text-sm sm:grid-cols-3">
              <div className="min-w-0">
                <div className="text-xs text-muted-foreground">出口地址</div>
                <div className="mt-1 truncate font-medium">
                  {proxyTestResult?.exit_ip || (proxyTestLoading ? '检测中...' : '未知')}
                </div>
              </div>
              <div className="min-w-0">
                <div className="text-xs text-muted-foreground">国家 / 地区</div>
                <ProxyCountryDisplay result={proxyTestResult} loading={proxyTestLoading} />
              </div>
              <div className="min-w-0">
                <div className="text-xs text-muted-foreground">代理状态</div>
                <div className="mt-1 flex items-center gap-2 font-medium">
                  {proxyTestLoading ? (
                    <>
                      <Loader2 className="size-4 animate-spin text-emerald-500" />
                      测速中
                    </>
                  ) : proxyTestError || proxyTestResult?.exit_error ? (
                    <>
                      <WifiOff className="size-4 text-rose-500" />
                      部分异常
                    </>
                  ) : (
                    <>
                      <Globe2 className="size-4 text-emerald-500" />
                      可用
                    </>
                  )}
                </div>
              </div>
            </div>
            {!proxyTestLoading && (proxyTestError || proxyTestResult?.exit_error) && (
              <div className="mt-3 rounded-md bg-rose-500/10 px-3 py-2 text-xs text-rose-600 dark:text-rose-300">
                {proxyTestError || proxyTestResult?.exit_error}
              </div>
            )}
          </div>

          {proxyTestResult?.targets?.length ? (
            <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
              {proxyTestResult.targets.map((target) => (
                <div
                  key={target.key}
                  className="flex min-h-[96px] flex-col justify-center rounded-lg border border-border/80 bg-background px-5 py-4 shadow-sm"
                >
                  <div className="flex min-w-0 items-center justify-between gap-4">
                    <div className="flex min-w-0 flex-1 items-center gap-3">
                      <ProxyTargetIcon target={target} />
                      <div className="flex min-w-0 items-center gap-2">
                        <span className="truncate text-[10px] font-semibold leading-none">{target.name}</span>
                      </div>
                    </div>
                    <div className={`shrink-0 text-[11px] font-bold leading-none tabular-nums ${proxyLatencyClass(target)}`}>
                      {formatProxyLatency(target)}
                    </div>
                  </div>
                  <div className="mt-4 flex gap-0.5 pl-2">
                    {Array.from({ length: target.total || 12 }).map((_, index) => (
                      <span
                        key={index}
                        className={`size-1.5 shrink-0 rounded-full ${proxyDotClass(target, index)}`}
                      />
                    ))}
                  </div>
                </div>
              ))}
            </div>
          ) : proxyTestLoading ? (
            <div className="flex min-h-56 items-center justify-center">
              <div className="flex flex-col items-center gap-3 text-sm text-muted-foreground">
                <Loader2 className="size-8 animate-spin text-emerald-500" />
                正在通过代理测速
              </div>
            </div>
          ) : (
            <div className="flex min-h-32 items-center justify-center rounded-lg border border-dashed text-sm text-muted-foreground">
              {proxyTestError ? '暂无测速结果' : '等待测速'}
            </div>
          )}

          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => void handleNetworkProxyTest()} disabled={proxyTestLoading || !proxyTestProxyURL}>
              {proxyTestLoading ? <Loader2 className="size-4 animate-spin" /> : <Gauge className="size-4" />}
              重新测试
            </Button>
            <Button type="button" onClick={() => setProxyTestOpen(false)}>关闭</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog
        open={telegramAgentModelDialogOpen}
        onOpenChange={(open) => {
          setTelegramAgentModelDialogOpen(open);
          if (!open) {
            setTelegramAgentModelDropdownOpen(false);
          }
        }}
      >
        <DialogContent className="w-[92vw] !max-w-3xl overflow-hidden p-0">
          <DialogHeader className="border-b border-border/70 px-5 py-4 pr-12">
            <DialogTitle className="flex items-center gap-2 text-base">
              <Settings className="size-4 text-emerald-600" />
              Agent 模型配置
            </DialogTitle>
          </DialogHeader>
          <div className="px-5 py-4">
            <Form {...telegramAgentForm}>
              <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
                <FormField
                  control={telegramAgentForm.control}
                  name="base_url"
                  render={({ field }) => (
                    <FormItem className="min-w-0 space-y-1">
                      <FormLabel className="text-xs text-muted-foreground">请求 URL</FormLabel>
                      <FormControl>
                        <Input
                          className="h-9"
                          placeholder="https://api.example.com/v1"
                          {...field}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={telegramAgentForm.control}
                  name="api_key"
                  render={({ field }) => (
                    <FormItem className="min-w-0 space-y-1">
                      <FormLabel className="text-xs text-muted-foreground">API Key</FormLabel>
                      <FormControl>
                        <div className="relative">
                          <Input
                            className="h-9 pr-10"
                            type={telegramAgentAPIKeyVisible ? 'text' : 'password'}
                            placeholder="sk-..."
                            autoComplete="off"
                            {...field}
                          />
                          <Button
                            type="button"
                            variant="ghost"
                            className="absolute right-1 top-1/2 h-7 w-7 -translate-y-1/2 p-0 text-muted-foreground hover:text-foreground"
                            onClick={() => setTelegramAgentAPIKeyVisible((visible) => !visible)}
                            aria-label={telegramAgentAPIKeyVisible ? '隐藏 API Key' : '显示 API Key'}
                            title={telegramAgentAPIKeyVisible ? '隐藏 API Key' : '显示 API Key'}
                          >
                            {telegramAgentAPIKeyVisible ? (
                              <EyeOff className="size-4" />
                            ) : (
                              <Eye className="size-4" />
                            )}
                          </Button>
                        </div>
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>

              <div className="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
                <FormField
                  control={telegramAgentForm.control}
                  name="model"
                  render={({ field }) => {
                    const keyword = String(field.value || '').trim().toLowerCase();
                    const filteredModels = keyword
                      ? telegramAgentModelOptions.filter((model) => model.id.toLowerCase().includes(keyword))
                      : telegramAgentModelOptions;
                    return (
                      <FormItem className="min-w-0 space-y-1 sm:col-span-2 lg:col-span-2">
                        <div className="flex items-center justify-between gap-2">
                          <FormLabel className="text-xs text-muted-foreground">对话模型</FormLabel>
                          <Button
                            type="button"
                            variant="outline"
                            className="h-7 gap-1.5 px-2 text-xs"
                            disabled={telegramAgentModelsLoading}
                            onClick={() => void handleFetchTelegramAgentModels()}
                          >
                            <RefreshCw className={telegramAgentModelsLoading ? 'size-3.5 animate-spin' : 'size-3.5'} />
                            {telegramAgentModelsLoading ? '获取中' : '获取模型'}
                          </Button>
                        </div>
                        <FormControl>
                          <div className="relative">
                            <Input
                              className="h-9"
                              placeholder="gpt-5.5"
                              {...field}
                              onChange={(event) => {
                                field.onChange(event.target.value);
                                if (telegramAgentModelOptions.length > 0) {
                                  setTelegramAgentModelDropdownOpen(true);
                                }
                              }}
                              onFocus={() => {
                                if (telegramAgentModelOptions.length > 0) {
                                  setTelegramAgentModelDropdownOpen(true);
                                }
                              }}
                              onBlur={() => {
                                window.setTimeout(() => setTelegramAgentModelDropdownOpen(false), 120);
                              }}
                            />
                            {telegramAgentModelDropdownOpen && telegramAgentModelOptions.length > 0 ? (
                              <div className="absolute left-0 right-0 top-full z-50 mt-1 max-h-44 overflow-y-auto rounded-md border border-border/70 bg-popover p-1 text-popover-foreground shadow-lg">
                                {filteredModels.length > 0 ? (
                                  filteredModels.slice(0, 100).map((model) => (
                                    <button
                                      key={model.id}
                                      type="button"
                                      className="flex h-8 w-full items-center rounded-sm px-2 text-left text-xs hover:bg-accent hover:text-accent-foreground"
                                      onMouseDown={(event) => {
                                        event.preventDefault();
                                        field.onChange(model.id);
                                        setTelegramAgentModelDropdownOpen(false);
                                      }}
                                      title={model.id}
                                    >
                                      <span className="truncate">{model.id}</span>
                                    </button>
                                  ))
                                ) : (
                                  <div className="px-2 py-2 text-xs text-muted-foreground">无匹配模型</div>
                                )}
                              </div>
                            ) : null}
                          </div>
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    );
                  }}
                />

                <FormField
                  control={telegramAgentForm.control}
                  name="max_history_messages"
                  render={({ field }) => (
                    <FormItem className="min-w-0 space-y-1">
                      <FormLabel className="text-xs text-muted-foreground">上下文数</FormLabel>
                      <FormControl>
                        <Input
                          className="h-9"
                          type="number"
                          min={1}
                          value={field.value}
                          onChange={(event) => field.onChange(Number(event.target.value))}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={telegramAgentForm.control}
                  name="max_tokens"
                  render={({ field }) => (
                    <FormItem className="min-w-0 space-y-1">
                      <FormLabel className="text-xs text-muted-foreground">输出 Tokens</FormLabel>
                      <FormControl>
                        <Input
                          className="h-9"
                          type="number"
                          min={1}
                          value={field.value}
                          onChange={(event) => field.onChange(Number(event.target.value))}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={telegramAgentForm.control}
                  name="edit_interval_ms"
                  render={({ field }) => (
                    <FormItem className="min-w-0 space-y-1">
                      <FormLabel className="text-xs text-muted-foreground">刷新间隔</FormLabel>
                      <FormControl>
                        <div className="relative">
                          <Input
                            className="h-9 pr-9"
                            type="number"
                            min={300}
                            value={field.value}
                            onChange={(event) => field.onChange(Number(event.target.value))}
                          />
                          <span className="pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-xs text-muted-foreground">
                            ms
                          </span>
                        </div>
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>
            </Form>
          </div>
          <DialogFooter className="border-t border-border/70 px-5 py-4">
            <Button type="button" variant="outline" onClick={() => setTelegramAgentModelDialogOpen(false)}>
              完成
            </Button>
            <Button type="submit" form="telegram-agent-form" disabled={telegramAgentSaving}>
              {telegramAgentSaving ? '保存中...' : '保存配置'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={telegramAgentPromptDialogOpen} onOpenChange={setTelegramAgentPromptDialogOpen}>
        <DialogContent className="flex h-[82vh] w-[92vw] !max-w-5xl flex-col overflow-hidden p-0">
          <DialogHeader className="border-b border-border/70 px-5 py-4 pr-12">
            <DialogTitle className="flex items-center gap-2 text-base">
              <Bot className="size-4 text-emerald-600" />
              系统提示词
            </DialogTitle>
          </DialogHeader>
          <div className="min-h-0 flex-1 px-5 py-4">
            <Form {...telegramAgentForm}>
              <FormField
                control={telegramAgentForm.control}
                name="system_prompt"
                render={({ field }) => (
                  <FormItem className="flex h-full flex-col space-y-2">
                    <FormControl>
                      <Textarea
                        className="h-full min-h-[56vh] resize-none overflow-y-auto text-sm leading-6"
                        {...field}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </Form>
          </div>
          <DialogFooter className="border-t border-border/70 px-5 py-4">
            <Button type="button" onClick={() => setTelegramAgentPromptDialogOpen(false)}>
              完成
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
