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
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";

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
    <div className="space-y-2">
      <div>
        <label className="block text-sm font-medium text-gray-700">
          LRAP firmware package
        </label>
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
          className="flex w-full cursor-pointer flex-col items-center justify-center rounded-xl border-2 border-dashed border-gray-300 bg-gray-50 px-6 py-10 text-center transition hover:border-blue-400 hover:bg-blue-50 disabled:cursor-not-allowed disabled:opacity-50"
        >
          <div className="mb-3 rounded-full bg-white p-3 shadow-sm">
            <FileUp className="h-7 w-7 text-blue-600" />
          </div>

          <div className="text-sm font-semibold text-gray-800">
            Click to select LRAP firmware package
          </div>

          <div className="mt-1 text-xs text-gray-500">
            Supported: lrap-fw-*.bin
          </div>
        </button>
      ) : (
        <div className="flex items-center justify-between gap-3 rounded-xl border border-blue-200 bg-blue-50 px-4 py-4">
          <div className="flex min-w-0 items-center gap-3">
            <div className="shrink-0 rounded-lg bg-white p-2 shadow-sm">
              <FileArchive className="h-6 w-6 text-blue-600" />
            </div>

            <div className="min-w-0">
              <div className="truncate text-sm font-semibold text-gray-900">
                {file.name}
              </div>
              <div className="mt-0.5 text-xs text-gray-500">
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
              className="border-red-200 text-red-600 hover:bg-red-50"
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
    setMessage("Uploading firmware package. The upgrade will start shortly.");
    setResults([]);

    try {
      const data = await uploadBundle(bundleFile, keepSettings, targetAPIPs);
      let lastResponse = data;

      setResults(Array.isArray(data.results) ? data.results : []);
      setMessage(data.message || "Package accepted. Upgrade job is running.");

      if (data.job_id) {
        while (true) {
          await sleep(3000);

          try {
            const status = await fetchFirmwareJob(data.job_id);
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
            const mainFlashStarted = hasACFlashStarted(lastResponse.results);

            if (mainFlashStarted) {
              startACRebootCountdown();
              break;
            }

            throw pollErr;
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
          <div className="flex w-full max-w-md flex-col items-center rounded-xl bg-white p-8 text-center shadow-2xl">
            <div className="relative mb-6 flex items-center justify-center">
              <svg className="h-32 w-32 -rotate-90 transform">
                <circle
                  cx="64"
                  cy="64"
                  r={circleRadius}
                  stroke="currentColor"
                  strokeWidth="8"
                  fill="transparent"
                  className="text-gray-200"
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
                  className="text-blue-600 transition-all duration-1000 ease-linear"
                />
              </svg>

              <div className="absolute flex flex-col items-center">
                <Power className="mb-1 h-7 w-7 animate-pulse text-blue-600" />
                <div className="text-2xl font-bold text-gray-800">
                  {acRebootCountdown}s
                </div>
              </div>
            </div>

            <h3 className="mb-2 text-2xl font-bold text-gray-900">
              Upgrade Started
            </h3>

            <div className="mb-5 rounded-lg border border-blue-200 bg-blue-50 p-4 text-left">
              <div className="flex items-start gap-2 text-sm text-blue-800">
                <AlertTriangle className="h-5 w-5 shrink-0" />
                <p>
                  The firmware upgrade is in progress. The portal and WiFi may
                  disconnect shortly. This is normal. Do not power off the
                  device.
                </p>
              </div>
            </div>

            <p className="mb-3 text-sm text-gray-600">
              After the countdown finishes, reconnect to the LRAP WiFi if needed
              and log in again.
            </p>

            <p className="text-sm font-semibold text-blue-700">
              Redirecting to login in {acRebootCountdown}s...
            </p>
          </div>
        </div>
      )}

      <div className="mx-auto max-w-6xl">
        <div className="mb-8">
          <h2 className="text-3xl font-bold text-gray-900">Firmware Upgrade</h2>
          <p className="mt-1 text-gray-500">
            Upload one firmware package to update the device.
          </p>
        </div>

        {message && (
          <div className="mb-6 rounded border border-blue-100 bg-blue-50 p-3 text-sm text-blue-700">
            {message}
          </div>
        )}

        <Card>
          <CardHeader className="border-b bg-gray-50/50">
            <div className="flex items-center gap-2">
              <PackageCheck className="h-5 w-5 text-blue-600" />
              <div>
                <CardTitle>LRAP Firmware Package</CardTitle>
              </div>
            </div>
          </CardHeader>

          <CardContent className="space-y-6 p-6">
            <FirmwarePicker
              file={bundleFile}
              disabled={flashing || acRebootCountdown !== null}
              onFileChange={setBundleFile}
            />

            <label className="flex items-center gap-2 text-sm text-gray-700">
              <input
                type="checkbox"
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
              Flash LRAP Firmware Package
            </Button>

            {flashing && (
              <div className="rounded-md border border-amber-100 bg-amber-50 p-3 text-sm text-amber-800">
                Upload has returned. This page is polling the background upgrade
                job every few seconds.
              </div>
            )}

            {visibleResults.length > 0 && (
              <div className="mt-4 rounded border">
                <div className="border-b bg-gray-50 px-3 py-2 text-sm font-semibold">
                  Firmware Upgrade Results
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
                          <div className="font-mono text-xs text-gray-500">
                            {result.version}
                          </div>
                        )}
                        {result.detail && (
                          <div className="mt-1 break-words text-xs text-gray-500">
                            {result.detail}
                          </div>
                        )}
                        {result.error && (
                          <div className="mt-1 break-words text-xs text-red-600">
                            {result.error}
                          </div>
                        )}
                      </div>

                      {result.status === "running" ? (
                        <span className="flex shrink-0 items-center gap-1 rounded bg-blue-100 px-2 py-1 text-xs text-blue-700">
                          <RefreshCw className="h-3 w-3 animate-spin" />
                          Waiting
                        </span>
                      ) : result.status === "failed" || !result.ok ? (
                        <span className="flex shrink-0 items-center gap-1 rounded bg-red-100 px-2 py-1 text-xs text-red-700">
                          <XCircle className="h-3 w-3" />
                          Failed
                        </span>
                      ) : (
                        <span className="flex shrink-0 items-center gap-1 rounded bg-green-100 px-2 py-1 text-xs text-green-700">
                          <CheckCircle className="h-3 w-3" />
                          OK
                        </span>
                      )}
                    </div>
                  ))}
                </div>
              </div>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
