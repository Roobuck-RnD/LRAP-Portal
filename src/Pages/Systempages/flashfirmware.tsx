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
  Cpu,
  Wifi,
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
  CardDescription,
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
  if (role === "sub") return `AP firmware${stage}`;
  if (role === "main") return `AC firmware${stage}`;
  if (role === "ac") return `AC firmware${stage}`;
  if (role === "ap") return `AP firmware${stage}`;
  if (role === "dhcp") return `DHCP leases${stage}`;

  return `${role}${stage}`;
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
        <p className="mt-0.5 text-xs text-gray-500">
          Upload one LRAP .bin package. It contains both AP/sub firmware and
          AC/main firmware.
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

  const acModule = useMemo<Module | undefined>(() => {
    return (
      currentAllModule.find((m) => m.type === "Main Module") ||
      currentAllModule.find((m) => m.port === "br-lan") ||
      currentAllModule[0]
    );
  }, [currentAllModule]);

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
      "AC firmware upgrade has started. The portal and WiFi may disconnect shortly. Please wait for the countdown to finish, then log in again.",
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

    const apText =
      apModules.length > 0
        ? apModules
            .map((ap) => `${ap.name || "RoobuckAP"} (${ap.ipaddress})`)
            .join("\n")
        : "No APs detected by the UI. The backend will fall back to 10.10.18.2-10.10.18.5.";

    const ok = await confirmDialog({
      title: "Flash firmware?",
      destructive: true,
      confirmText: "Flash",
      description:
      `Flash LRAP firmware package?\n\n` +
        `Package: ${bundleFile.name}\n` +
        `Size: ${formatFileSize(bundleFile.size)}\n` +
        `Keep current configuration: ${keepSettings ? "Yes" : "No"}\n\n` +
        `Upgrade order:\n` +
        `1. Validate and unpack the LRAP package on the AC.\n` +
        `2. Flash AP/sub firmware first.\n` +
        `3. Wait for APs to come back with the expected version.\n` +
        `4. Flash AC/main firmware last.\n\n` +
        `Target AC:\n${acModule?.name || "Main Module"} ${acModule?.ipaddress || "Local AC"}\n\n` +
        `Target APs:\n${apText}\n\n` +
        `${keepSettings ? "" : "WARNING: Current AC/AP settings will be erased.\n\n"}` +
        `The upgrade can take several minutes. Do not close this page until the AC upgrade starts.`,
    });

    if (!ok) return;

    setFlashing(true);
    setMessage(
      "Uploading LRAP package. The AC will validate, flash APs first, then flash itself.",
    );
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
                ? "LRAP firmware upgrade has started. The AC will reboot shortly."
                : "LRAP firmware upgrade did not complete."),
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
              AC Upgrade Started
            </h3>

            <div className="mb-5 rounded-lg border border-blue-200 bg-blue-50 p-4 text-left">
              <div className="flex items-start gap-2 text-sm text-blue-800">
                <AlertTriangle className="h-5 w-5 shrink-0" />
                <p>
                  The AC firmware upgrade is in progress. The portal and WiFi
                  may disconnect shortly. This is normal. Do not power off the
                  AC or AP modules.
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
            Upload one LRAP firmware package. The system upgrades AP modules
            first, clears AP DHCP leases, then upgrades AC last.
          </p>
        </div>

        <div className="mb-6 rounded-xl border border-amber-200 bg-amber-50 p-4 text-sm text-amber-800">
          <div className="flex gap-2">
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
            <div>
              <div className="font-semibold">Important</div>
              <p className="mt-1">
                Upload only the combined LRAP package, for example
                lrap-fw-2026.06.30.bin. This is not a normal OpenWrt sysupgrade
                image and must be uploaded through this portal, not LuCI
                firmware flash.
              </p>
            </div>
          </div>
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
                <CardDescription>
                  One upload contains both AP/sub firmware and AC/main firmware.
                </CardDescription>
              </div>
            </div>
          </CardHeader>

          <CardContent className="space-y-6 p-6">
            <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
              <div className="rounded border bg-white p-4">
                <div className="mb-2 flex items-center gap-2 text-sm font-semibold text-gray-900">
                  <Cpu className="h-4 w-4 text-blue-600" />
                  AC / Main Module
                </div>
                <div className="text-sm text-gray-800">
                  {acModule?.name || "Main Module"}
                </div>
                <div className="font-mono text-xs text-gray-500">
                  {acModule?.ipaddress || "Local AC"}
                </div>
                <div className="mt-2 text-xs text-gray-500">
                  Flashed last after AP verification.
                </div>
              </div>

              <div className="rounded border bg-white p-4">
                <div className="mb-2 flex items-center gap-2 text-sm font-semibold text-gray-900">
                  <Wifi className="h-4 w-4 text-purple-600" />
                  AP / Sub Modules
                </div>

                {apModules.length === 0 ? (
                  <div className="text-xs text-amber-700">
                    No AP modules detected in the UI. Backend fallback targets
                    10.10.18.2-10.10.18.5.
                  </div>
                ) : (
                  <div className="space-y-1">
                    {apModules.map((ap) => (
                      <div
                        key={ap.ipaddress}
                        className="flex justify-between gap-3 text-xs"
                      >
                        <span className="truncate text-gray-800">
                          {ap.name || "RoobuckAP"}
                        </span>
                        <span className="font-mono text-gray-500">
                          {ap.ipaddress}
                        </span>
                      </div>
                    ))}
                  </div>
                )}

                <div className="mt-2 text-xs text-gray-500">
                  Flashed first, DHCP leases are cleared, then APs are verified
                  before AC.
                </div>
              </div>
            </div>

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
              Keep current configuration on AC and AP modules
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
                        <div className="font-mono text-xs text-gray-500">
                          {result.ip ||
                            (result.role === "main" ? "AC" : "Package")}
                          {result.version ? ` · ${result.version}` : ""}
                        </div>
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
