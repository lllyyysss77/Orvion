import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

export function openExternalUrl(rawUrl: string | null | undefined): boolean {
  if (typeof window === "undefined") {
    return false
  }
  const value = (rawUrl ?? "").trim()
  if (!value) {
    return false
  }

  let parsed: URL
  try {
    parsed = new URL(value)
  } catch {
    return false
  }

  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    return false
  }

  const popup = window.open(parsed.toString(), "_blank", "noopener,noreferrer")
  if (popup) {
    popup.opener = null
  }
  return popup !== null
}
