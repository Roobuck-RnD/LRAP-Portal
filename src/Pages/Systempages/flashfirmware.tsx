import type { JSX } from "react";
import { useEffect, useMemo, useRef, useState } from "react";
import {
  Upload,
  RefreshCw,
  AlertTriangle,
  CheckCircle,
  XCircle,
  FileUp,
  FileArchive,
  X,
  PackageCheck,
  Power,
} from "lucide-react";
import { toast } from "sonner";
import { useCurrentAllModuleStore } from "@/states/allModuleState";
import { apiFetch } from "@/utils/http";
import { confirmDialog } from "@/components/ui/confirm";
import { Button } from "@/components/ui/button";

type Module = {
  name: string;
  ipaddress: string;
  type: string;
  port?: string;
};

type FlashResult = {
  ip?: string;
  name?: string;
  role?: string;
  stage?: string;
  status?: "running" | "ok" | "failed" | string;
  ok: boolean;
  detail?: string;
  error?: string;
  version?: string;
};

type FlashResponse = {
  ok: boolean;
  message?: string;
  target_type?: "bundle" | "ac" | "ap" | string;
  version?: string;
  job_id?: string;
  status?: string;
  done?: boolean;
  results?: FlashResult[];
};

const AC_UPGRADE_REBOOT_DELAY = 240;

// 轮询进度的兜底上限,防止进度页无限卡在 waiting。
// 总上限 > 后端每 AP 8 分钟验证超时 + AC 重启预算。
const FLASH_POLL_MAX_TOTAL_MS = 15 * 60 * 1000;
// 尚未进入 AC 自刷阶段就断连:重试这么久仍连不上 → 判为无法确认/失败,不再干等。
const FLASH_POLL_DISCONNECT_GIVEUP_MS = 90 * 1000;

type FirmwarePickerProps = {
  file: File | null;
  disabled?: boolean;
  onFileChange: (file: File | null) => void;
};

function getErrorMessage(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => window.setTimeout(resolve, ms));
}

function formatFileSize(bytes: number): string {
  if (!bytes) return "0 B";

  const k = 1024;
  const sizes = ["B", "KB", "MB", "GB"];
  const i = Math.min(
    Math.floor(Math.log(bytes) / Math.log(k)),
    sizes.length - 1,
  );

  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(2))} ${sizes[i]}`;
}

function resultLabel(result: FlashResult): string {
  const role = result.role || "firmware";
  const stage = result.stage ? ` / ${result.stage}` : "";

  if (role === "bundle") return `Package${stage}`;
  if (role === "dhcp") return `DHCP leases${stage}`;

  // 不区分 AC/AP,统一显示为固件,避免暴露多模块架构
  return `Firmware${stage}`;
}

function hasACFlashStarted(items?: FlashResult[]): boolean {
  return (items || []).some((item) => {
    const role = item.role || "";
    const stage = item.stage || "";
    return item.ok && (role === "main" || role === "ac") && stage === "flash";
  });
}

function shouldHideResult(
  result: FlashResult,
  allResults: FlashResult[],
): boolean {
  const role = result.role || "";
  const stage = result.stage || "";
  const ip = result.ip || "";

  // The backend appends a running "wait" row before verification starts.
  // Once the same AP has a terminal verify row, keep the final verify row and hide
  // the old spinner row so the UI does not show Waiting and OK at the same time.
  if (
    ip &&
    (role === "sub" || role === "ap") &&
    stage === "wait" &&
    result.status === "running"
  ) {
    return allResults.some((item) => {
      const itemRole = item.role || "";
      return (
        item.ip === ip &&
        (itemRole === "sub" || itemRole === "ap") &&
        item.stage === "verify" &&
        item.status !== "running"
      );
    });
  }

  return false;
}

function FirmwarePicker({
  file,
  disabled,
  onFileChange,
}: FirmwarePickerProps): JSX.Element {
  const inputRef = useRef<HTMLInputElement | null>(null);

  const openPicker = () => {
    if (disabled) return;
    inputRef.current?.click();
  };

  const clearFile = () => {
    if (disabled) return;

    if (inputRef.current) {
      inputRef.current.value = "";
    }

    onFileChange(null);
  };

  return (
    <div className="space-y-3">
      <div>
        <h3 className="font-display text-base font-semibold tracking-tight text-foreground">
          Firmware package
        </h3>
        <p className="mt-0.5 text-sm text-muted-foreground">
          Select the package file, then start the flash.
        </p>
      </div>

      <input
        ref={inputRef}
        type="file"
        accept=".bin"
        className="hidden"
        disabled={disabled}
        onChange={(e) => onFileChange(e.target.files?.[0] || null)}
      />

      {!file ? (
        <button
          type="button"
          onClick={openPicker}
          disabled={disabled}
          className="flex w-full cursor-pointer flex-col items-center justify-center rounded-lg border-2 border-dashed border-border bg-muted/40 px-6 py-12 text-center transition hover:border-primary/50 hover:bg-primary/10 disabled:cursor-not-allowed disabled:opacity-50"
        >
          <div className="mb-3 rounded-full bg-card p-3 shadow-sm">
            <FileUp className="h-7 w-7 text-primary" />
          </div>

          <div className="text-sm font-semibold text-foreground">
            Click to select LRAP firmware package
          </div>

          <div className="mt-1 text-xs text-muted-foreground">
            Supported: lrap-fw-*.bin
          </div>
        </button>
      ) : (
        <div className="flex items-center justify-between gap-3 rounded-lg border border-primary/30 bg-primary/10 px-4 py-4">
          <div className="flex min-w-0 items-center gap-3">
            <div className="shrink-0 rounded-lg bg-card p-2 shadow-sm">
              <FileArchive className="h-6 w-6 text-primary" />
            </div>

            <div className="min-w-0">
              <div className="truncate text-sm font-semibold text-foreground">
                {file.name}
              </div>
              <div className="mt-0.5 text-xs text-muted-foreground">
                {formatFileSize(file.size)}
              </div>
            </div>
          </div>

          <div className="flex shrink-0 gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={openPicker}
              disabled={disabled}
            >
              Change
            </Button>

            <Button
              variant="outline"
              size="sm"
              onClick={clearFile}
              disabled={disabled}
              className="border-destructive/30 text-destructive hover:bg-destructive/10"
            >
              <X className="h-4 w-4" />
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}

export default function FlashFirmware(): JSX.Element {
  const { currentAllModule } = useCurrentAllModuleStore();

  const apModules = useMemo<Module[]>(() => {
    return currentAllModule
      .filter((m) => m.type !== "Main Module")
      .filter((m) => !!m.ipaddress);
  }, [currentAllModule]);

  const targetAPIPs = useMemo(() => {
    const ips = apModules.map((ap) => ap.ipaddress).filter(Boolean);

    // Backend has a static fallback, but sending the currently discovered APs makes
    // the operation explicit and keeps this UI compatible with existing module discovery.
    return ips;
  }, [apModules]);

  const [bundleFile, setBundleFile] = useState<File | null>(null);
  const [keepSettings, setKeepSettings] = useState(true);
  const [flashing, setFlashing] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const [results, setResults] = useState<FlashResult[]>([]);
  const [acRebootCountdown, setAcRebootCountdown] = useState<number | null>(
    null,
  );
  // 烧录进度弹窗:开始烧录即打开,烧录中不可关闭,结束(成功/失败)后才出现 Confirm。
  const [flashLogOpen, setFlashLogOpen] = useState(false);

  const visibleResults = useMemo(() => {
    return results.filter((result) => !shouldHideResult(result, results));
  }, [results]);

  const startACRebootCountdown = () => {
    setAcRebootCountdown((current) => current ?? AC_UPGRADE_REBOOT_DELAY);
    setMessage(
      "Firmware upgrade has started. The portal and WiFi may disconnect shortly. Please wait for the countdown to finish, then log in again.",
    );
  };

  useEffect(() => {
    if (acRebootCountdown === null) return;

    if (acRebootCountdown <= 0) {
      sessionStorage.clear()
      const cacheBust = Date.now()
      window.location.replace(`/?fw_reload=${cacheBust}#/login`)
      return;
    }

    const timer = window.setTimeout(() => {
      setAcRebootCountdown((current) => {
        if (current === null) return null;
        return current - 1;
      });
    }, 1000);

    return () => window.clearTimeout(timer);
  }, [acRebootCountdown]);

  const acRebootProgress =
    acRebootCountdown === null
      ? 0
      : ((AC_UPGRADE_REBOOT_DELAY - acRebootCountdown) /
          AC_UPGRADE_REBOOT_DELAY) *
        100;
  const circleRadius = 40;
  const circumference = 2 * Math.PI * circleRadius;

  const uploadBundle = async (
    file: File,
    keepCurrentSettings: boolean,
    targetIPs: string[],
  ) => {
    const formData = new FormData();
    formData.append("target_type", "bundle");
    formData.append("keep_settings", keepCurrentSettings ? "1" : "0");
    formData.append("firmware", file);
    formData.append("target_ips", targetIPs.join(","));

    const res = await apiFetch("/api/system/firmware/flash", {
      method: "POST",
      body: formData,
    });

    if (!res.ok) {
      const text = await res.text().catch(() => "");
      throw new Error(text || `HTTP ${res.status}`);
    }

    return (await res.json()) as FlashResponse;
  };

  const fetchFirmwareJob = async (jobID: string) => {
    const res = await apiFetch(
      `/api/system/firmware/flash?job_id=${encodeURIComponent(jobID)}`,
      {
        method: "GET",
      },
    );

    if (!res.ok) {
      const text = await res.text().catch(() => "");
      throw new Error(text || `HTTP ${res.status}`);
    }

    return (await res.json()) as FlashResponse;
  };

  const handleFlashBundle = async () => {
    if (!bundleFile) {
      toast.error("Please select an LRAP firmware package.");
      return;
    }

    const ok = await confirmDialog({
      title: "Flash firmware?",
      destructive: true,
      confirmText: "Flash",
      description:
        `Flash LRAP firmware package?\n\n` +
        `Package: ${bundleFile.name}\n` +
        `Size: ${formatFileSize(bundleFile.size)}\n` +
        `Keep current configuration: ${keepSettings ? "Yes" : "No"}\n\n` +
        `${keepSettings ? "" : "WARNING: Current settings will be erased.\n\n"}` +
        `The upgrade can take several minutes. Do not close this page until it starts.`,
    });

    if (!ok) return;

    setFlashing(true);
    setFlashLogOpen(true);
    setMessage("Uploading firmware package. The upgrade will start shortly.");
    setResults([]);

    try {
      const data = await uploadBundle(bundleFile, keepSettings, targetAPIPs);
      let lastResponse = data;

      setResults(Array.isArray(data.results) ? data.results : []);
      setMessage(data.message || "Package accepted. Upgrade job is running.");

      if (data.job_id) {
        const loopStart = Date.now();
        let lastContact = Date.now();

        while (true) {
          await sleep(3000);

          // 总看门狗上限:超时仍未到终态,停止并明确判失败,绝不无限卡 waiting。
          if (Date.now() - loopStart > FLASH_POLL_MAX_TOTAL_MS) {
            setMessage(
              "Firmware upgrade timed out: could not confirm the result within the expected time. Please check the device and its version manually.",
            );
            break;
          }

          try {
            const status = await fetchFirmwareJob(data.job_id);
            lastContact = Date.now();
            lastResponse = status;

            const nextResults = Array.isArray(status.results)
              ? status.results
              : [];
            setResults(nextResults);
            setMessage(status.message || "Firmware upgrade job is running.");

            if (
              hasACFlashStarted(nextResults) ||
              status.status === "ac_rebooting"
            ) {
              startACRebootCountdown();
              break;
            }

            if (status.done || status.status === "failed") {
              break;
            }
          } catch (pollErr: unknown) {
            // 已进入 AC 自刷阶段后断连 = 预期内的 AC 重启 → 进倒计时,别当失败。
            if (
              hasACFlashStarted(lastResponse.results) ||
              lastResponse.status === "ac_rebooting"
            ) {
              startACRebootCountdown();
              break;
            }

            // 尚未到 AC 自刷就断连:可能是中途重启/网络波动。不首次就抛错冻结,
            // 显示"重连中"继续有限重试;超过放弃阈值仍连不上 → 判无法确认/失败。
            if (Date.now() - lastContact > FLASH_POLL_DISCONNECT_GIVEUP_MS) {
              setMessage(
                "Lost connection to the device during the upgrade and could not reconnect. It may be rebooting, or the upgrade may have failed — reconnect and re-check the version.",
              );
              break;
            }

            setMessage(
              "Connection to the device was interrupted (it may be rebooting). Waiting to reconnect…",
            );
          }
        }
      } else {
        if (hasACFlashStarted(data.results)) {
          startACRebootCountdown();
        } else {
          setMessage(
            data.message ||
              (data.ok
                ? "Firmware upgrade has started. The device will reboot shortly."
                : "Firmware upgrade did not complete."),
          );
        }
      }
    } catch (err: unknown) {
      setMessage(`LRAP firmware upgrade failed: ${getErrorMessage(err)}`);
    } finally {
      setFlashing(false);
    }
  };

  return (
    <div className="relative w-full p-6">
      {acRebootCountdown !== null && (
        <div className="fixed inset-0 z-50 flex flex-col items-center justify-center bg-black/80 backdrop-blur-sm transition-all">
          <div className="flex w-full max-w-md flex-col items-center rounded-xl bg-card p-8 text-center shadow-2xl">
            <div className="relative mb-6 flex items-center justify-center">
              <svg className="h-32 w-32 -rotate-90 transform">
                <circle
                  cx="64"
                  cy="64"
                  r={circleRadius}
                  stroke="currentColor"
                  strokeWidth="8"
                  fill="transparent"
                  className="text-muted-foreground/40"
                />
                <circle
                  cx="64"
                  cy="64"
                  r={circleRadius}
                  stroke="currentColor"
                  strokeWidth="8"
                  fill="transparent"
                  strokeDasharray={circumference}
                  strokeDashoffset={
                    circumference - (acRebootProgress / 100) * circumference
                  }
                  className="text-primary transition-all duration-1000 ease-linear"
                />
              </svg>

              <div className="absolute flex flex-col items-center">
                <Power className="mb-1 h-7 w-7 animate-pulse text-primary" />
                <div className="text-2xl font-bold text-foreground">
                  {acRebootCountdown}s
                </div>
              </div>
            </div>

            <h3 className="mb-2 text-2xl font-bold text-foreground">
              Upgrade Started
            </h3>

            <div className="mb-5 rounded-lg border border-primary/30 bg-primary/10 p-4 text-left">
              <div className="flex items-start gap-2 text-sm text-primary">
                <AlertTriangle className="h-5 w-5 shrink-0" />
                <p>
                  The firmware upgrade is in progress. The portal and WiFi may
                  disconnect shortly. This is normal. Do not power off the
                  device.
                </p>
              </div>
            </div>

            <p className="mb-3 text-sm text-muted-foreground">
              After the countdown finishes, reconnect to the LRAP WiFi if needed
              and log in again.
            </p>

            <p className="text-sm font-semibold text-primary">
              Redirecting to login in {acRebootCountdown}s...
            </p>
          </div>
        </div>
      )}

      {flashLogOpen && acRebootCountdown === null && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-background/70 p-4 backdrop-blur-sm">
          <div className="flex max-h-[85vh] w-full max-w-lg flex-col overflow-hidden rounded-xl bg-card shadow-2xl">
            <div className="flex items-center gap-2 border-b px-5 py-4">
              {flashing ? (
                <RefreshCw className="h-5 w-5 animate-spin text-primary" />
              ) : (
                <PackageCheck className="h-5 w-5 text-primary" />
              )}
              <h3 className="text-lg font-semibold text-foreground">
                Firmware Upgrade
              </h3>
            </div>

            <div className="flex-1 overflow-y-auto px-5 py-4">
              {message && (
                <div className="mb-4 rounded border border-primary/20 bg-primary/10 p-3 text-sm text-primary">
                  {message}
                </div>
              )}

              {visibleResults.length > 0 ? (
                <div className="rounded border">
                  <div className="border-b bg-muted px-3 py-2 text-sm font-semibold">
                    Progress
                  </div>
                  <div className="divide-y">
                    {visibleResults.map((result, idx) => (
                      <div
                        key={`${result.role || "result"}-${result.stage || ""}-${result.ip || idx}`}
                        className="flex items-center justify-between gap-3 px-3 py-2 text-sm"
                      >
                        <div className="min-w-0">
                          <div className="font-medium">{resultLabel(result)}</div>
                          {result.version && (
                            <div className="font-mono text-xs text-muted-foreground">
                              {result.version}
                            </div>
                          )}
                          {result.detail && (
                            <div className="mt-1 break-words text-xs text-muted-foreground">
                              {result.detail}
                            </div>
                          )}
                          {result.error && (
                            <div className="mt-1 break-words text-xs text-destructive">
                              {result.error}
                            </div>
                          )}
                        </div>

                        {result.status === "running" ? (
                          <span className="flex shrink-0 items-center gap-1 rounded bg-primary/15 px-2 py-1 text-xs text-primary">
                            <RefreshCw className="h-3 w-3 animate-spin" />
                            Working
                          </span>
                        ) : result.status === "failed" || !result.ok ? (
                          <span className="flex shrink-0 items-center gap-1 rounded bg-destructive/15 px-2 py-1 text-xs text-destructive">
                            <XCircle className="h-3 w-3" />
                            Failed
                          </span>
                        ) : (
                          <span className="flex shrink-0 items-center gap-1 rounded bg-success/15 px-2 py-1 text-xs text-success">
                            <CheckCircle className="h-3 w-3" />
                            OK
                          </span>
                        )}
                      </div>
                    ))}
                  </div>
                </div>
              ) : (
                <div className="text-sm text-muted-foreground">Starting…</div>
              )}
            </div>

            <div className="border-t bg-muted px-5 py-4">
              {flashing ? (
                <p className="text-center text-sm text-muted-foreground">
                  Upgrade in progress — please do not close this window or power
                  off the device.
                </p>
              ) : (
                <div className="flex justify-end">
                  <Button onClick={() => setFlashLogOpen(false)}>Confirm</Button>
                </div>
              )}
            </div>
          </div>
        </div>
      )}

      <div className="mx-auto max-w-6xl">
        <div className="mb-8">
          <h2 className="text-3xl font-bold text-foreground">Firmware Upgrade</h2>
          <p className="mt-1 text-muted-foreground">
            Upload one firmware package to update the device.
          </p>
        </div>

        {!flashLogOpen && message && (
          <div className="mb-6 rounded border border-primary/20 bg-primary/10 p-3 text-sm text-primary">
            {message}
          </div>
        )}

        <div className="rounded-lg border border-border bg-card p-6">
          <FirmwarePicker
            file={bundleFile}
            disabled={flashing || acRebootCountdown !== null}
            onFileChange={setBundleFile}
          />

          <div className="mt-6 flex flex-col gap-4 border-t border-border pt-5 sm:flex-row sm:items-center sm:justify-between">
            <label className="flex cursor-pointer items-center gap-2 text-sm text-foreground">
              <input
                type="checkbox"
                className="size-4 accent-primary"
                checked={keepSettings}
                onChange={(e) => setKeepSettings(e.target.checked)}
                disabled={flashing || acRebootCountdown !== null}
              />
              Keep current configuration
            </label>

            <Button
              onClick={() => void handleFlashBundle()}
              disabled={flashing || acRebootCountdown !== null || !bundleFile}
            >
              {flashing ? (
                <RefreshCw className="mr-2 h-4 w-4 animate-spin" />
              ) : (
                <Upload className="mr-2 h-4 w-4" />
              )}
              Flash Firmware
            </Button>
          </div>
        </div>
      </div>
    </div>
  );
}
