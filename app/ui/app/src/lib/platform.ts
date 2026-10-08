export function isWindowsPlatform(): boolean {
  if (typeof window !== "undefined" && window.ROSE_PLATFORM) {
    return window.ROSE_PLATFORM === "windows";
  }

  return (
    typeof navigator !== "undefined" &&
    navigator.platform.toLowerCase().includes("win")
  );
}
