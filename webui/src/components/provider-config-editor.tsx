import { forwardRef, useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Trash2, Eye, EyeOff } from "lucide-react";

export type ConfigValueType = "string" | "number" | "boolean" | "json";

export type ConfigItem = {
	id: string;
	key: string;
	type: ConfigValueType;
	value: string;
	locked?: boolean;
};

type Props = {
	value: string;
	onChange: (nextJson: string) => void;
	providerType?: string;
};

export type ProviderConfigEditorRef = {
	addItem: () => void;
};

const BASE_DEFAULT_ITEMS: Omit<ConfigItem, "id">[] = [
	{ key: "base_url", type: "string", value: "", locked: true },
	{ key: "api_key", type: "string", value: "", locked: true },
];

function defaultItemsByType(providerType?: string): Omit<ConfigItem, "id">[] {
	if (providerType === "anthropic") {
		return BASE_DEFAULT_ITEMS.concat([
			// 对应请求头 anthropic-version
			{ key: "version", type: "string", value: "2023-06-01", locked: true },
		]);
	}
	return BASE_DEFAULT_ITEMS;
}

function newId() {
	return typeof crypto !== "undefined" && "randomUUID" in crypto
		? crypto.randomUUID()
		: `${Date.now()}-${Math.random().toString(16).slice(2)}`;
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
			const t: ConfigValueType =
				typeof v === "boolean" ? "boolean" :
				typeof v === "number" ? "number" :
				typeof v === "string" ? "string" :
				"json";

			const value = t === "json" ? JSON.stringify(v, null, 2) : String(v);
			baseItems.push({ id: newId(), key: k, type: t, value, locked: false });
			seen.add(k);
		}

		// 将默认字段的值覆盖为 JSON 中的实际值（若存在）
		for (const item of baseItems) {
			if (!Object.prototype.hasOwnProperty.call(parsed, item.key)) continue;
			const v = (parsed as Record<string, unknown>)[item.key];
			if (item.key === "version") {
				item.type = "string";
				item.value = String(v ?? "");
			} else {
				item.type = "string";
				item.value = String(v ?? "");
			}
		}

		return baseItems;
	} catch {
		return defaults.map(item => ({ ...item, id: newId() }));
	}
}

function serializeItems(items: ConfigItem[], providerType?: string): { json: string; error: string | null } {
	const obj: Record<string, unknown> = {};
	for (const item of items) {
		const key = item.key.trim();
		if (!key) return { json: "", error: "存在空的配置键名" };

		if (Object.prototype.hasOwnProperty.call(obj, key)) {
			return { json: "", error: `存在重复的配置键名: ${key}` };
		}

		if (item.type === "boolean") {
			obj[key] = item.value === "true";
			continue;
		}

		if (item.type === "number") {
			const n = Number(item.value);
			if (!Number.isFinite(n)) return { json: "", error: `字段 ${key} 不是合法数字` };
			obj[key] = n;
			continue;
		}

		if (item.type === "json") {
			try {
				obj[key] = JSON.parse(item.value || "null");
			} catch {
				return { json: "", error: `字段 ${key} 的 JSON 格式不正确` };
			}
			continue;
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
	{ value, onChange, providerType },
	ref,
) {
	const defaults = useMemo(() => defaultItemsByType(providerType), [providerType]);

	const lastEmittedRef = useRef<string | null>(null);
	const lastDefaultsRef = useRef<Omit<ConfigItem, "id">[]>(defaults);
	const [items, setItems] = useState<ConfigItem[]>(() => normalizeJsonToItems(value, defaults));
	const [visibleApiKeyIds, setVisibleApiKeyIds] = useState<Record<string, boolean>>({});

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
		setItems(prev => prev.concat([{ id: newId(), key: "", type: "string", value: "" }]));
	}, []);

	const removeItem = (id: string) => {
		setItems(prev => prev.filter(item => item.id !== id));
	};

	const toggleApiKeyVisibility = (id: string) => {
		setVisibleApiKeyIds(prev => ({ ...prev, [id]: !prev[id] }));
	};

	useImperativeHandle(ref, () => ({ addItem }), [addItem]);

	return (
		<div className="space-y-3">
			{error && <div className="text-xs text-destructive">{error}</div>}

			<div className="grid grid-cols-12 gap-2 text-xs text-muted-foreground">
				<div className="col-span-4">键</div>
				<div className="col-span-3">类型</div>
				<div className="col-span-4">值</div>
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

						<div className="col-span-3">
							<Select
								value={item.type}
								onValueChange={(v) => updateItem(item.id, { type: v as ConfigValueType })}
								disabled={item.locked}
							>
								<SelectTrigger>
									<SelectValue placeholder="选择类型" />
								</SelectTrigger>
								<SelectContent>
									<SelectItem value="string">字符串</SelectItem>
									<SelectItem value="number">数字</SelectItem>
									<SelectItem value="boolean">布尔</SelectItem>
									<SelectItem value="json">JSON</SelectItem>
								</SelectContent>
							</Select>
						</div>

						<div className="col-span-4">
							{item.type === "boolean" ? (
								<div className="flex items-center gap-2 h-10">
									<Switch
										checked={item.value === "true"}
										onCheckedChange={(checked) => updateItem(item.id, { value: String(checked) })}
										aria-label="布尔值"
									/>
									<span className="text-xs text-muted-foreground">
										{item.value === "true" ? "true" : "false"}
									</span>
								</div>
							) : item.type === "json" ? (
								<Textarea
									value={item.value}
									onChange={(e) => updateItem(item.id, { value: e.target.value })}
									// 避免长 JSON 行（例如 api_key）导致布局溢出
									className="resize-none w-full max-w-full min-w-0 whitespace-pre-wrap break-all overflow-x-auto min-h-24 [field-sizing:fixed]"
									placeholder='例如: {"foo":"bar"} 或 ["a","b"]'
									aria-label="JSON 值"
								/>
							) : (
								(() => {
									const isApiKeyField = item.key.trim().toLowerCase() === "api_key";
									const showApiKey = Boolean(visibleApiKeyIds[item.id]);
									const inputType = item.type === "number" ? "number" : (isApiKeyField && !showApiKey ? "password" : "text");
									return (
										<div className="relative">
											<Input
												type={inputType}
												value={item.value}
												onChange={(e) => updateItem(item.id, { value: e.target.value })}
												placeholder={
													item.key === "version"
														? "例如: 2023-06-01（对应 anthropic-version）"
														: `请输入 ${item.key || "值"}`
												}
												aria-label="值"
												className={isApiKeyField ? "pr-10" : undefined}
											/>
											{isApiKeyField && (
												<Button
													type="button"
													variant="ghost"
													size="icon"
													className="absolute right-1 top-1/2 size-8 -translate-y-1/2"
													onClick={() => toggleApiKeyVisibility(item.id)}
													aria-label={showApiKey ? "隐藏 API Key" : "显示 API Key"}
												>
													{showApiKey ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
												</Button>
											)}
										</div>
									);
								})()
							)}
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
