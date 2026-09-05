type ApiMethod = "GET" | "POST" | "PUT" | "DELETE" | "PATCH";

type ApiContent =
  | Blob
  | File
  | Pick<ReadableStreamDefaultReader<any>, "read">
  | "";

interface ApiOpts {
  method?: ApiMethod;
  headers?: object;
  body?: any;
  signal?: AbortSignal;
}

interface TusSettings {
  retryCount: number;
  chunkSize: number;
}

type ChecksumAlg = "md5" | "sha1" | "sha256" | "sha512";

interface Share {
  hash: string;
  path: string;
  expire?: any;
  userID?: number;
  hasPassword?: boolean;
  username?: string;
}

interface SearchParams {
  [key: string]: string;
}

type LinkDownloadDownloader = "auto" | "direct" | "yt-dlp";
type LinkDownloadStatus =
  | "queued"
  | "running"
  | "completed"
  | "failed"
  | "canceled";

interface LinkDownloadSettings {
  enabled: boolean;
  defaultPath: string;
  defaultQuality: string;
  downloader: LinkDownloadDownloader;
  ytdlpAvailable: boolean;
}

interface LinkDownloadYTDLPUpdate {
  version?: string;
  output?: string;
}

interface LinkDownloadQualityOption {
  resolution?: number;
  recommended?: boolean;
  advanced?: boolean;
  audioOnly?: boolean;
  audioVariants?: Record<
    string,
    { quality: string; description: string; technicalDetails?: string }
  >;
  technicalDetails?: string;
  description?: string;
  label: string;
  quality: string;
}

interface LinkDownloadQualities {
  audioLanguages?: string[];
  subtitleLanguages?: Array<{ language: string; automatic: boolean }>;
  verified: boolean;
  notice?: string;
  downloader: LinkDownloadDownloader;
  options: LinkDownloadQualityOption[];
  error?: string;
}

interface LinkDownloadRequest {
  container?: "mkv" | "mp4";
  subtitleLanguage?: string;
  url: string;
  path: string;
  filename?: string;
  quality: string;
  downloader: LinkDownloadDownloader;
  overwrite: boolean;
}

interface LinkDownloadJob {
  id: string;
  url: string;
  path: string;
  name: string;
  quality: string;
  downloader: LinkDownloadDownloader;
  status: LinkDownloadStatus;
  error?: string;
  progress: number;
  bytesReceived: number;
  bytesTotal: number;
  startedAt: string;
  finishedAt?: string;
}
