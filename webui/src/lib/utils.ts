import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

export function openExternalUrl(rawUrl: string | null | undefined): boolean {
  if (typeof window === "undefined" || typeof document === "undefined") {
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

  const link = document.createElement("a")
  link.href = parsed.toString()
  link.target = "_blank"
  link.rel = "noopener noreferrer"
  link.style.display = "none"
  document.body.appendChild(link)
  link.click()
  link.remove()
  return true
}
