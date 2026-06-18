import { fetchJSON, fetchURL } from "./utils";

export async function settings() {
  return fetchJSON<LinkDownloadSettings>("/api/downloads/settings", {});
}

export async function qualities(
  url: string,
  downloader: LinkDownloadDownloader
) {
  const params = new URLSearchParams({ url, downloader });
  return fetchJSON<LinkDownloadQualities>(
    `/api/downloads/qualities?${params}`,
    {}
  );
}

export async function create(request: LinkDownloadRequest) {
  const res = await fetchURL("/api/downloads", {
    method: "POST",
    body: JSON.stringify(request),
  });
  return (await res.json()) as LinkDownloadJob;
}

export async function get(id: string) {
  return fetchJSON<LinkDownloadJob>(`/api/downloads/${id}`, {});
}

export async function cancel(id: string) {
  await fetchURL(`/api/downloads/${id}`, {
    method: "DELETE",
  });
}
