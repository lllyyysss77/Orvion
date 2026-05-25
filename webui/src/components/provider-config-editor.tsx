import { forwardRef, useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Textarea } from "@/components/ui/textarea";
import { KeyRound, Trash2 } from "lucide-react";

export type ConfigItem = {
	id: string;
	key: string;
	value: string;
	locked?: boolean;
};

type Props = {
	value: string;
	onChange: (nextJson: string) => void;
};

export type ProviderConfigEditorRef = {
	addItem: () => void;
};

const BASE_DEFAULT_ITEMS: Omit<ConfigItem, "id">[] = [
	{ key: "base_url", value: "", locked: true },
	{ key: "api_key", value: "", locked: true },
];

function defaultItemsByType(providerType?: string): Omit<ConfigItem, "id">[] {
	if (providerType === "anthropic") {
		return BASE_DEFAULT_ITEMS.concat([
			{ key: "version", value: "2023-06-01", locked: true },
		]);
	}
	return BASE_DEFAULT_ITEMS;
}

function newId() {
	return typeof crypto !== "undefined" && "randomUUID" in crypto
		? crypto.randomUUID()
		: `${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function splitApiKeys(raw: string): string[] {
	return raw
		.replace(/[，,]/g, "\n")
		.split(/\r?\n/)
		.map(item => item.trim())
		.filter(Boolean);
}

function formatApiKeyLines(raw: string): string {
	return splitApiKeys(raw).join("\n");
}

function formatApiKeyValue(raw: string): string {
	return splitApiKeys(raw).join(",");
}

function normalizeApiKeyDraft(raw: string): string {
	return raw.replace(/[，,]/g, "\n");
}

function normalizeJsonToItems(raw: string, defaults: Omit<ConfigItem, "id">[]): ConfigItem[] {
	if (!raw) {
		return defaults.map(item => ({ ...item, id: newId() }));
	}
	try {
		const parsed = JSON.parse(raw);
		if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
			throw new Error("not-object");
		}

		const baseItems: ConfigItem[] = defaults.map(item => ({
			...item,
			id: newId(),
		}));

		const seen = new Set<string>(baseItems.map(i => i.key));

		// 保留 JSON 中存在但不在默认字段中的额外字段
		for (const [k, v] of Object.entries(parsed)) {
			if (seen.has(k)) continue;
			const value = typeof v === "string" ? v : JSON.stringify(v);
			baseItems.push({ id: newId(), key: k, value: value ?? "", locked: false });
			seen.add(k);
		}

		// 将默认字段的值覆盖为 JSON 中的实际值（若存在）
		for (const item of baseItems) {
			if (!Object.prototype.hasOwnProperty.call(parsed, item.key)) continue;
			const v = (parsed as Record<string, unknown>)[item.key];
			item.value = typeof v === "string" ? v : JSON.stringify(v);
		}

		return baseItems;
	} catch {
		return defaults.map(item => ({ ...item, id: newId() }));
	}
}

function inferProviderTypeFromConfig(raw: string): string | undefined {
	try {
		const parsed = JSON.parse(raw || "{}") as Record<string, unknown>;
		const version = String(parsed.version ?? "").trim();
		if (version) {
			return "anthropic";
		}
		const baseURL = String(parsed.base_url ?? "").trim().toLowerCase();
		if (baseURL.includes("anthropic")) {
			return "anthropic";
		}
		if (
			baseURL.includes("generativelanguage.googleapis.com") ||
			baseURL.includes("googleapis.com/v1beta") ||
			baseURL.includes("googleapis.com/v1alpha")
		) {
			return "gemini";
		}
	} catch {
		return undefined;
	}
	return undefined;
}

function serializeItems(items: ConfigItem[], providerType?: string): { json: string; error: string | null } {
	const obj: Record<string, unknown> = {};
	for (const item of items) {
		const key = item.key.trim();
		if (!key) return { json: "", error: "存在空的配置键名" };

		if (Object.prototype.hasOwnProperty.call(obj, key)) {
			return { json: "", error: `存在重复的配置键名: ${key}` };
		}

		obj[key] = item.value ?? "";
	}

	if (providerType === "anthropic") {
		const version = String(obj["version"] ?? "").trim();
		if (!version) return { json: "", error: "Anthropic 必须配置 anthropic-version（字段名：version）" };
	}

	return { json: JSON.stringify(obj, null, 2), error: null };
}

const ProviderConfigEditor = forwardRef<ProviderConfigEditorRef, Props>(function ProviderConfigEditor(
	{ value, onChange },
	ref,
) {
	const providerType = useMemo(() => inferProviderTypeFromConfig(value), [value]);
	const defaults = useMemo(() => defaultItemsByType(providerType), [providerType]);

	const lastEmittedRef = useRef<string | null>(null);
	const lastDefaultsRef = useRef<Omit<ConfigItem, "id">[]>(defaults);
	const [items, setItems] = useState<ConfigItem[]>(() => normalizeJsonToItems(value, defaults));
	const [apiKeyDrafts, setApiKeyDrafts] = useState<Record<string, string>>({});
	const [apiKeyPopoverOpen, setApiKeyPopoverOpen] = useState<Record<string, boolean>>({});

	useEffect(() => {
		// 外部变化（例如切换类型/打开编辑不同 provider）：同步到内部状态
		const defaultsChanged = defaults !== lastDefaultsRef.current;
		lastDefaultsRef.current = defaults;

		// 如果 value 没变且 defaults 也没变，跳过
		if (value === lastEmittedRef.current && !defaultsChanged) return;

		// 使用当前的 value（包含用户已填的数据）重新解析，保留额外字段
		const normalized = normalizeJsonToItems(value, defaults);
		setItems(normalized);
		lastEmittedRef.current = value;
	}, [value, defaults]);

	const { json, error } = useMemo(() => serializeItems(items, providerType), [items, providerType]);

	useEffect(() => {
		if (error) return;
		lastEmittedRef.current = json;
		onChange(json);
	}, [json, error, onChange]);

	const updateItem = (id: string, patch: Partial<ConfigItem>) => {
		setItems(prev => prev.map(item => (item.id === id ? { ...item, ...patch } : item)));
	};

	const addItem = useCallback(() => {
		setItems(prev => prev.concat([{ id: newId(), key: "", value: "" }]));
	}, []);

	const removeItem = (id: string) => {
		setItems(prev => prev.filter(item => item.id !== id));
	};

	const handleApiKeyPopoverOpen = (item: ConfigItem, open: boolean) => {
		setApiKeyPopoverOpen(prev => ({ ...prev, [item.id]: open }));
		if (open) {
			setApiKeyDrafts(prev => ({ ...prev, [item.id]: formatApiKeyLines(item.value) }));
		}
	};

	const updateApiKeyDraft = (id: string, value: string) => {
		setApiKeyDrafts(prev => ({ ...prev, [id]: normalizeApiKeyDraft(value) }));
	};

	const saveApiKeys = (id: string) => {
		const nextValue = formatApiKeyValue(apiKeyDrafts[id] ?? "");
		updateItem(id, { value: nextValue });
		setApiKeyPopoverOpen(prev => ({ ...prev, [id]: false }));
	};

	useImperativeHandle(ref, () => ({ addItem }), [addItem]);

	return (
		<div className="space-y-3">
			{error && <div className="text-xs text-destructive">{error}</div>}

			<div className="grid grid-cols-12 gap-2 text-xs text-muted-foreground">
				<div className="col-span-4">键</div>
				<div className="col-span-7">值</div>
				<div className="col-span-1 text-right">操作</div>
			</div>

			<div className="space-y-3">
				{items.map(item => (
					<div key={item.id} className="grid grid-cols-12 gap-2 items-end">
						<div className="col-span-4">
							<Input
								value={item.key}
								disabled={item.locked}
								onChange={(e) => updateItem(item.id, { key: e.target.value })}
								placeholder="例如: base_url"
								aria-label="键"
							/>
						</div>

						<div className="col-span-7">
							{(() => {
								const isApiKeyField = item.key.trim().toLowerCase() === "api_key";
								if (isApiKeyField) {
									const keyCount = splitApiKeys(item.value).length;
									const draft = apiKeyDrafts[item.id] ?? formatApiKeyLines(item.value);
									return (
										<div className="flex min-w-0 items-center gap-2">
											<div className="min-w-0 flex-1 rounded-md border bg-muted/35 px-3 py-2 text-sm text-muted-foreground">
												{keyCount > 0 ? `已配置 ${keyCount} 个 Key` : "未配置 Key"}
											</div>
											<Popover
												modal={true}
												open={Boolean(apiKeyPopoverOpen[item.id])}
												onOpenChange={(open) => handleApiKeyPopoverOpen(item, open)}
											>
												<PopoverTrigger asChild>
													<Button type="button" variant="outline" className="shrink-0">
														<KeyRound className="size-4" />
														编辑 Key
													</Button>
												</PopoverTrigger>
												<PopoverContent className="w-[min(520px,calc(100vw-2rem))] space-y-3" align="end">
													<div className="space-y-1">
														<div className="text-sm font-medium">API Key</div>
														<Textarea
															value={draft}
															onChange={(event) => updateApiKeyDraft(item.id, event.target.value)}
															className="min-h-48 resize-none font-mono text-xs leading-5"
															placeholder="每行一个 Key"
														/>
													</div>
													<div className="flex justify-end gap-2">
														<Button
															type="button"
															variant="outline"
															onClick={() => handleApiKeyPopoverOpen(item, false)}
														>
															取消
														</Button>
														<Button type="button" onClick={() => saveApiKeys(item.id)}>
															保存
														</Button>
													</div>
												</PopoverContent>
											</Popover>
										</div>
									);
								}
								const placeholder = isApiKeyField
									? "多个 Key 可用逗号分隔，系统会轮询使用"
									: item.key === "version"
										? "例如: 2023-06-01（对应 anthropic-version）"
										: `请输入 ${item.key || "值"}`;
								return (
									<div className="relative">
										<Input
											type="text"
											value={item.value}
											onChange={(e) => updateItem(item.id, { value: e.target.value })}
											placeholder={placeholder}
											aria-label="值"
										/>
									</div>
								);
							})()}
						</div>

						<div className="col-span-1 flex justify-end">
							<Button
								type="button"
								variant="ghost"
								size="icon"
								disabled={item.locked}
								onClick={() => removeItem(item.id)}
								title="删除字段"
							>
								<Trash2 className="size-4" />
							</Button>
						</div>
					</div>
				))}
			</div>
		</div>
	);
});

export default ProviderConfigEditor;
