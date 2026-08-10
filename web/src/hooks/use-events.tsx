import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import type { Download } from "@/api/client";
import { mergeDownloadSnapshot, type StreamEvent } from "@/lib/download-events";

export type { StreamEvent } from "@/lib/download-events";

type StreamState = {
  status: "disabled" | "connected" | "reconnecting";
  lastEvent: StreamEvent | null;
  sessionBytes: number;
};

const EventStreamContext = createContext<StreamState>({
  status: "disabled",
  lastEvent: null,
  sessionBytes: 0,
});

export function EventStreamProvider({
  enabled,
  children,
}: {
  enabled: boolean;
  children: ReactNode;
}) {
  const queryClient = useQueryClient();
  const [status, setStatus] = useState<StreamState["status"]>(
    enabled ? "reconnecting" : "disabled",
  );
  const [lastEvent, setLastEvent] = useState<StreamEvent | null>(null);
  const [sessionBytes, setSessionBytes] = useState(0);
  const sessionTotals = useRef(new Map<string, number>());

  useEffect(() => {
    if (!enabled || typeof EventSource === "undefined") {
      setStatus("disabled");
      sessionTotals.current.clear();
      setSessionBytes(0);
      return;
    }

    setStatus("reconnecting");
    let hadDisconnect = false;
    const source = new EventSource("/api/v1/events", { withCredentials: true });
    const eventNames = [
      "job.updated",
      "file.updated",
      "speed.updated",
      "queue.updated",
      "account.updated",
      "settings.updated",
    ];
    const handleOpen = () => {
      setStatus("connected");
      if (hadDisconnect) {
        void queryClient.invalidateQueries({ queryKey: ["downloads"] });
        void queryClient.invalidateQueries({ queryKey: ["download-events"] });
        void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
        void queryClient.invalidateQueries({ queryKey: ["accounts"] });
        void queryClient.invalidateQueries({ queryKey: ["proxies"] });
        void queryClient.invalidateQueries({ queryKey: ["settings"] });
      }
      hadDisconnect = false;
    };
    const handleError = () => {
      hadDisconnect = true;
      setStatus("reconnecting");
      // EventSource performs the reconnect itself. Refetching here repairs
      // the authoritative snapshot without adding periodic browser requests.
      void queryClient.invalidateQueries({ queryKey: ["downloads"] });
      void queryClient.invalidateQueries({ queryKey: ["download-events"] });
      void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
    };
    const handleEvent = (raw: Event) => {
      const message = raw as MessageEvent<string>;
      let event: StreamEvent;
      try {
        event = JSON.parse(message.data) as StreamEvent;
      } catch {
        return;
      }
      const payload = event.data ?? {};
      const jobEvent =
        event.name === "job.updated" ||
        event.name === "file.updated" ||
        event.name === "speed.updated";
      const jobId = jobEvent
        ? (event.jobId ?? stringValue(payload.downloadId) ?? stringValue(payload.id))
        : undefined;
      if (jobId) {
        if (payload.deleted === true) {
          queryClient.removeQueries({ queryKey: ["downloads", jobId], exact: true });
        }
        patchDownloadCaches(queryClient, jobId, event, payload);
        if (event.name === "job.updated") {
          void queryClient.invalidateQueries({ queryKey: ["download-events", jobId] });
          const current = queryClient.getQueryData<Download[]>(["downloads"]);
          if (payload.deleted !== true && current && !current.some((item) => item.id === jobId)) {
            void queryClient.invalidateQueries({ queryKey: ["downloads"] });
          }
        }
      }
      if (event.name === "speed.updated" && jobId) {
        const total =
          typeof payload.sessionBytes === "number" && Number.isFinite(payload.sessionBytes)
            ? payload.sessionBytes
            : undefined;
        if (total !== undefined) {
          const previous = sessionTotals.current.get(jobId) ?? 0;
          if (total >= previous) {
            sessionTotals.current.set(jobId, total);
            let sum = 0;
            for (const value of sessionTotals.current.values()) sum += value;
            setSessionBytes(sum);
          }
        }
      }
      if (event.name === "queue.updated") {
        void queryClient.invalidateQueries({ queryKey: ["downloads"] });
        void queryClient.invalidateQueries({ queryKey: ["dashboard"] });
      } else if (event.name === "account.updated") {
        void queryClient.invalidateQueries({ queryKey: ["accounts"] });
      } else if (event.name === "settings.updated") {
        void queryClient.invalidateQueries({ queryKey: ["settings"] });
        // Proxy CRUD uses the settings event namespace so credentials and
        // profile labels are not exposed in the global stream.
        void queryClient.invalidateQueries({ queryKey: ["proxies"] });
      }
      setLastEvent(event);
    };

    source.addEventListener("open", handleOpen);
    source.addEventListener("error", handleError);
    for (const name of eventNames) source.addEventListener(name, handleEvent);
    return () => {
      source.removeEventListener("open", handleOpen);
      source.removeEventListener("error", handleError);
      for (const name of eventNames) source.removeEventListener(name, handleEvent);
      source.close();
    };
  }, [enabled, queryClient]);

  return (
    <EventStreamContext.Provider value={{ status, lastEvent, sessionBytes }}>
      {children}
    </EventStreamContext.Provider>
  );
}

export function useEventStream() {
  return useContext(EventStreamContext);
}

function patchDownloadCaches(
  queryClient: ReturnType<typeof useQueryClient>,
  jobId: string,
  event: StreamEvent,
  payload: Record<string, unknown>,
) {
  queryClient.setQueryData<Download[]>(["downloads"], (current) => {
    if (!current) return current;
    if (payload.deleted === true) return current.filter((download) => download.id !== jobId);
    return current.map((download) =>
      download.id === jobId ? mergeDownloadSnapshot(download, event, payload) : download,
    );
  });
  queryClient.setQueryData<Download>(["downloads", jobId], (current) => {
    if (!current || payload.deleted === true) return current;
    return mergeDownloadSnapshot(current, event, payload);
  });
}

function stringValue(value: unknown): string | undefined {
  return typeof value === "string" ? value : undefined;
}
