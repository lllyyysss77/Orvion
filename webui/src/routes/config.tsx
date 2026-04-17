import { useState, useEffect, useCallback } from 'react';
import { zodResolver } from '@hookform/resolvers/zod';
import { useForm } from 'react-hook-form';
import { z } from 'zod';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
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
  configAPI,
  type AnthropicProxyIPConfig,
  type TelegramBreakerAlertConfig,
  type ModelPriceSyncConfig,
  type SystemLogCleanupConfig,
} from '@/lib/api';
import { toast } from 'sonner';
import { Settings, Network, Coins, FileClock, Type, Send } from 'lucide-react';

const anthropicProxySchema = z.object({
  enabled: z.boolean(),
  proxy_ip: z.string().trim(),
}).refine((data) => !data.enabled || data.proxy_ip.length > 0, {
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
  proxy_url: z.string().trim(),
}).refine((data) => !data.enabled || data.bot_token.length > 0, {
  message: '启用 TG 告警时必须填写 Bot Token',
  path: ['bot_token'],
}).refine((data) => !data.enabled || data.chat_id.length > 0, {
  message: '启用 TG 告警时必须填写 Chat ID',
  path: ['chat_id'],
}).refine((data) => data.api_base.length === 0 || isValidURL(data.api_base), {
  message: 'TG API 地址格式不正确',
  path: ['api_base'],
}).refine((data) => data.proxy_url.length === 0 || isValidURL(data.proxy_url), {
  message: '代理 URL 格式不正确',
  path: ['proxy_url'],
});

const systemLogCleanupSchema = z.object({
  enabled: z.boolean(),
  interval_minutes: z.number().min(1, { message: '清理间隔必须大于 0' }),
});

const uiFontSchema = z.object({
  font: z.enum(['default', 'kunming_seagull', 'fenyuan', 'lxgw_wenkai']),
});

type AnthropicProxyForm = z.infer<typeof anthropicProxySchema>;
type TelegramBreakerAlertForm = z.infer<typeof telegramBreakerAlertSchema>;
type PriceSyncForm = z.infer<typeof priceSyncSchema>;
type SystemLogCleanupForm = z.infer<typeof systemLogCleanupSchema>;
type UIFontForm = z.infer<typeof uiFontSchema>;

const UI_FONT_STORAGE_KEY = "orvion_ui_font";

const resolveUIFont = (font?: string): UIFontForm['font'] => {
  if (font === 'kunming_seagull' || font === 'fenyuan' || font === 'lxgw_wenkai') {
    return font;
  }
  return 'default';
};

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
  const proxyForm = useForm<AnthropicProxyForm>({
    resolver: zodResolver(anthropicProxySchema),
    defaultValues: {
      enabled: false,
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
      proxy_url: '',
    },
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

  const fetchConfig = useCallback(async () => {
    try {
      setLoading(true);
      // 获取全局代理 IP 配置
      const proxyResponse = await configAPI.getConfig('anthropic_proxy_ip');
      if (proxyResponse.value) {
        const proxyCfg = JSON.parse(proxyResponse.value) as AnthropicProxyIPConfig;
        const nextProxyConfig = {
          enabled: Boolean(proxyCfg.enabled),
          proxy_ip: proxyCfg.proxy_ip || '',
        };
        proxyForm.reset(nextProxyConfig);
      }
    } catch (error) {
      console.error('Failed to load config:', error);
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
          proxy_url: tgCfg.proxy_url || '',
        };
        telegramBreakerAlertForm.reset(nextTGConfig);
      }
    } catch (error) {
      console.error('Failed to load telegram breaker alert config:', error);
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

    setLoading(false);
  }, [priceSyncForm, proxyForm, systemLogCleanupForm, telegramBreakerAlertForm, uiFontForm]);

  useEffect(() => {
    void fetchConfig();
  }, [fetchConfig]);

  const onProxySubmit = async (values: AnthropicProxyForm) => {
    try {
      await configAPI.updateConfig('anthropic_proxy_ip', values);
      toast.success('全局代理 IP 配置已保存');
    } catch (error) {
      console.error('Failed to save anthropic proxy config:', error);
      toast.error('保存全局代理 IP 配置失败');
    }
  };

  const onPriceSyncSubmit = async (values: PriceSyncForm) => {
    try {
      await configAPI.updateConfig('model_price_sync', values);
      toast.success('模型价格同步配置已保存');
    } catch (error) {
      console.error('Failed to save model price sync config:', error);
      toast.error('保存模型价格同步配置失败');
    }
  };

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

  const onSystemLogCleanupSubmit = async (values: SystemLogCleanupForm) => {
    try {
      await configAPI.updateConfig('system_log_cleanup', values);
      toast.success('系统日志自动清理配置已保存');
    } catch (error) {
      console.error('Failed to save system log cleanup config:', error);
      toast.error('保存系统日志自动清理配置失败');
    }
  };

  const onUIFontSubmit = async (values: UIFontForm) => {
    try {
      await configAPI.updateConfig('ui_font', values);
      applyUIFontSetting(values.font);
      toast.success('界面字体配置已保存');
    } catch (error) {
      console.error('Failed to save ui font config:', error);
      toast.error('保存界面字体配置失败');
    }
  };

  const handleRunPriceSync = async () => {
    try {
      setPriceSyncing(true);
      await configAPI.runModelPriceSync();
      toast.success('模型价格已开始同步');
    } catch (error) {
      console.error('Failed to run model price sync:', error);
      toast.error('模型价格同步失败');
    } finally {
      setPriceSyncing(false);
    }
  };

  if (loading) {
    return <Loading />;
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
          <Card className="rounded-2xl border border-border/60 bg-card/90 shadow-[0_18px_45px_rgba(0,0,0,0.08)]">
            <CardHeader className="pb-2">
              <CardTitle className="flex items-center gap-2 text-sm font-semibold">
                <Network className="size-4 text-emerald-600" />
                全局代理 IP 配置
              </CardTitle>
              <CardDescription className="text-xs">
                用于覆盖所有接口转发请求的 X-Forwarded-For 与 X-Real-IP
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <Form {...proxyForm}>
                <form id="proxy-form" onSubmit={proxyForm.handleSubmit(onProxySubmit)} className="space-y-4">
                  <FormField
                    control={proxyForm.control}
                    name="enabled"
                    render={({ field }) => (
                      <FormItem className="flex items-center justify-between gap-3 rounded-lg border border-border/60 bg-muted/50 px-3 py-2">
                        <FormLabel className="text-xs text-muted-foreground">启用代理 IP</FormLabel>
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
                    control={proxyForm.control}
                    name="proxy_ip"
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>代理 IP</FormLabel>
                        <FormControl>
                          <Input placeholder="203.0.113.10" {...field} />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                </form>
              </Form>
            </CardContent>
            <CardFooter className="flex justify-between">
              <Button type="submit" form="proxy-form">保存配置</Button>
            </CardFooter>
          </Card>

          <Card className="rounded-2xl border border-border/60 bg-card/90 shadow-[0_18px_45px_rgba(0,0,0,0.08)]">
            <CardHeader className="pb-2">
              <CardTitle className="flex items-center gap-2 text-sm font-semibold">
                <Send className="size-4 text-emerald-600" />
                TG 告警配置
              </CardTitle>
              <CardDescription className="text-xs">
                配置熔断告警发送到 Telegram 的 Bot 参数与发送代理。保存后可在 TG 对话中发送 /status 或 /help。
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
                      <FormItem>
                        <FormLabel>Bot Token</FormLabel>
                        <FormControl>
                          <Input type="password" placeholder="123456789:AA..." {...field} />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={telegramBreakerAlertForm.control}
                    name="chat_id"
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>Chat ID</FormLabel>
                        <FormControl>
                          <Input placeholder="-1001234567890" {...field} />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={telegramBreakerAlertForm.control}
                    name="api_base"
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>TG API 地址</FormLabel>
                        <FormControl>
                          <Input placeholder="https://api.telegram.org" {...field} />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={telegramBreakerAlertForm.control}
                    name="proxy_url"
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>发送代理 URL（可选）</FormLabel>
                        <FormControl>
                          <Input placeholder="http://127.0.0.1:7890 或 socks5://127.0.0.1:1080" {...field} />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
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

          <Card className="rounded-2xl border border-border/60 bg-card/90 shadow-[0_18px_45px_rgba(0,0,0,0.08)]">
            <CardHeader className="pb-2">
              <CardTitle className="flex items-center gap-2 text-sm font-semibold">
                <Coins className="size-4 text-emerald-600" />
                模型价格同步配置
              </CardTitle>
              <CardDescription className="text-xs">
                配置模型价格表的定时同步间隔
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <Form {...priceSyncForm}>
                <form id="price-sync-form" onSubmit={priceSyncForm.handleSubmit(onPriceSyncSubmit)} className="space-y-4">
                  <FormField
                    control={priceSyncForm.control}
                    name="enabled"
                    render={({ field }) => (
                      <FormItem className="flex items-center justify-between gap-3 rounded-lg border border-border/60 bg-muted/50 px-3 py-2">
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

                  <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                    <FormField
                      control={priceSyncForm.control}
                      name="interval_minutes"
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>执行间隔（分钟）</FormLabel>
                          <FormControl>
                            <Input
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
                      control={priceSyncForm.control}
                      name="source_url"
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>数据源</FormLabel>
                          <FormControl>
                            <Input placeholder="https://models.dev/api.json" {...field} />
                          </FormControl>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                  </div>
                </form>
              </Form>
            </CardContent>
            <CardFooter className="flex justify-between">
              <Button type="button" variant="outline" onClick={handleRunPriceSync} disabled={priceSyncing}>
                {priceSyncing ? '正在拉取...' : '立刻拉取'}
              </Button>
              <Button type="submit" form="price-sync-form">保存配置</Button>
            </CardFooter>
          </Card>

          <Card className="rounded-2xl border border-border/60 bg-card/90 shadow-[0_18px_45px_rgba(0,0,0,0.08)]">
            <CardHeader className="pb-2">
              <CardTitle className="flex items-center gap-2 text-sm font-semibold">
                <FileClock className="size-4 text-emerald-600" />
                系统日志自动清理
              </CardTitle>
              <CardDescription className="text-xs">
                定时清空系统日志文件内容，避免日志文件持续增大
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <Form {...systemLogCleanupForm}>
                <form id="system-log-cleanup-form" onSubmit={systemLogCleanupForm.handleSubmit(onSystemLogCleanupSubmit)} className="space-y-4">
                  <FormField
                    control={systemLogCleanupForm.control}
                    name="enabled"
                    render={({ field }) => (
                      <FormItem className="flex items-center justify-between gap-3 rounded-lg border border-border/60 bg-muted/50 px-3 py-2">
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
                      <FormItem>
                        <FormLabel>清理间隔（分钟）</FormLabel>
                        <FormControl>
                          <Input
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
                </form>
              </Form>
            </CardContent>
            <CardFooter className="justify-end">
              <Button type="submit" form="system-log-cleanup-form">保存配置</Button>
            </CardFooter>
          </Card>

          <Card className="rounded-2xl border border-border/60 bg-card/90 shadow-[0_18px_45px_rgba(0,0,0,0.08)]">
            <CardHeader className="pb-2">
              <CardTitle className="flex items-center gap-2 text-sm font-semibold">
                <Type className="size-4 text-emerald-600" />
                界面字体配置
              </CardTitle>
              <CardDescription className="text-xs">
                当前支持默认字体、昆明海鸥体、粉圆体和霞鹜文楷。选择昆明海鸥体后，正文会略微放大。
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <Form {...uiFontForm}>
                <form id="ui-font-form" onSubmit={uiFontForm.handleSubmit(onUIFontSubmit)} className="space-y-4">
                  <FormField
                    control={uiFontForm.control}
                    name="font"
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>字体</FormLabel>
                        <FormControl>
                          <Select value={field.value} onValueChange={(value) => field.onChange(value as UIFontForm['font'])}>
                            <SelectTrigger className="h-9 w-full bg-white">
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
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                </form>
              </Form>
            </CardContent>
            <CardFooter className="justify-end">
              <Button type="submit" form="ui-font-form">保存配置</Button>
            </CardFooter>
          </Card>
        </div>
      </div>
    </div>
  );
}
