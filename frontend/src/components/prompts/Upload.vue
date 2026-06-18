<template>
  <div class="card floating" :class="{ 'upload-link-card': mode === 'link' }">
    <div class="card-title">
      <h2>
        {{ mode === "link" ? t("linkDownload.title") : t("prompts.upload") }}
      </h2>
    </div>

    <div v-if="mode === 'choice'" class="card-content">
      <p>{{ t("prompts.uploadMessage") }}</p>
    </div>

    <div v-if="mode === 'choice'" class="card-action full">
      <div
        @click="uploadFile"
        @keypress.enter="uploadFile"
        class="action"
        id="focus-prompt"
        tabindex="1"
      >
        <i class="material-icons">insert_drive_file</i>
        <div class="title">{{ t("buttons.file") }}</div>
      </div>
      <div
        @click="uploadFolder"
        @keypress.enter="uploadFolder"
        class="action"
        tabindex="2"
      >
        <i class="material-icons">folder</i>
        <div class="title">{{ t("buttons.folder") }}</div>
      </div>
      <div
        v-if="linkSettings?.enabled"
        @click="openLinkDownload"
        @keypress.enter="openLinkDownload"
        class="action"
        tabindex="3"
      >
        <i class="material-icons">link</i>
        <div class="title">{{ t("buttons.link") }}</div>
      </div>
    </div>

    <form v-else @submit.prevent="startLinkDownload">
      <div class="card-content link-download">
        <p>
          <label for="link-download-url">{{ t("linkDownload.url") }}</label>
          <input
            id="link-download-url"
            ref="linkInput"
            class="input input--block"
            type="url"
            required
            v-model.trim="linkForm.url"
          />
        </p>

        <p>
          <label for="link-download-path">{{
            t("linkDownload.destination")
          }}</label>
          <input
            id="link-download-path"
            class="input input--block"
            type="text"
            v-model.trim="linkForm.path"
          />
        </p>

        <p>
          <label for="link-download-filename">{{
            t("linkDownload.filename")
          }}</label>
          <input
            id="link-download-filename"
            class="input input--block"
            type="text"
            v-model.trim="linkForm.filename"
          />
        </p>

        <p>
          <label for="link-download-quality">{{
            t("linkDownload.quality")
          }}</label>
          <select
            id="link-download-quality"
            class="input input--block"
            v-model="selectedQuality"
            :disabled="loadingQualityOptions"
          >
            <option
              v-for="option in qualityOptions"
              :key="option.quality"
              :value="option.quality"
            >
              {{ option.label }}
            </option>
            <option value="custom">
              {{ t("linkDownload.qualityCustom") }}
            </option>
          </select>
          <input
            v-if="selectedQuality === 'custom'"
            class="input input--block"
            type="text"
            required
            :placeholder="t('linkDownload.formatSelectorPlaceholder')"
            v-model.trim="customQuality"
          />
          <span v-if="selectedQuality === 'custom'" class="small">
            {{ t("linkDownload.formatSelectorHelp") }}
          </span>
          <span v-if="loadingQualityOptions" class="small">
            {{ t("linkDownload.loadingQualities") }}
          </span>
          <span v-else-if="qualityOptionsError" class="small">
            {{ qualityOptionsError }}
          </span>
        </p>

        <p>
          <label for="link-download-downloader">{{
            t("linkDownload.downloader")
          }}</label>
          <select
            id="link-download-downloader"
            class="input input--block"
            v-model="linkForm.downloader"
          >
            <option value="auto">{{ t("linkDownload.downloaderAuto") }}</option>
            <option value="yt-dlp">
              {{ t("linkDownload.downloaderYTDLP") }}
            </option>
            <option value="direct">
              {{ t("linkDownload.downloaderDirect") }}
            </option>
          </select>
        </p>

        <p>
          <input
            id="link-download-overwrite"
            type="checkbox"
            v-model="linkForm.overwrite"
          />
          <label for="link-download-overwrite">{{
            t("linkDownload.overwrite")
          }}</label>
        </p>

        <div v-if="job" class="link-download-progress">
          <progress-bar
            :val="Math.max(0, Math.min(job.progress || 0, 100))"
            size="small"
            :text="progressText"
          />
          <p v-if="job.error" class="small">{{ job.error }}</p>
        </div>
      </div>

      <div class="card-action">
        <button
          v-if="!job || isTerminal"
          class="button button--flat"
          type="button"
          @click="mode = 'choice'"
        >
          {{ t("buttons.cancel") }}
        </button>
        <button
          v-else
          class="button button--flat button--red"
          type="button"
          @click="cancelLinkDownload"
        >
          {{ t("buttons.cancel") }}
        </button>
        <input
          class="button button--flat"
          type="submit"
          :disabled="submitting || (!!job && !isTerminal)"
          :value="t('buttons.downloadFromLink')"
        />
      </div>
    </form>
  </div>
</template>

<script setup lang="ts">
import {
  computed,
  inject,
  nextTick,
  onBeforeUnmount,
  onMounted,
  reactive,
  ref,
  watch,
} from "vue";
import { useI18n } from "vue-i18n";
import { useRoute } from "vue-router";
import { downloads } from "@/api";
import { removePrefix } from "@/api/utils";
import ProgressBar from "@/components/ProgressBar.vue";
import { useFileStore } from "@/stores/file";
import { useLayoutStore } from "@/stores/layout";

import * as upload from "@/utils/upload";

const { t } = useI18n();
const route = useRoute();

const layoutStore = useLayoutStore();
const fileStore = useFileStore();

const $showError = inject<IToastError>("$showError")!;
const $showSuccess = inject<IToastSuccess>("$showSuccess")!;

const mode = ref<"choice" | "link">("choice");
const linkSettings = ref<LinkDownloadSettings | null>(null);
const linkInput = ref<HTMLInputElement | null>(null);
const submitting = ref(false);
const job = ref<LinkDownloadJob | null>(null);
const pollTimer = ref<number | null>(null);
const qualityOptions = ref<LinkDownloadQualityOption[]>([]);
const defaultQuality = "bestvideo*+bestaudio/best";
const selectedQuality = ref(defaultQuality);
const customQuality = ref("");
const loadingQualityOptions = ref(false);
const qualityOptionsError = ref("");
const qualityFetchTimer = ref<number | null>(null);
const qualityFetchSerial = ref(0);

const linkForm = reactive<LinkDownloadRequest>({
  url: "",
  path: "/",
  filename: "",
  quality: defaultQuality,
  downloader: "auto",
  overwrite: false,
});

const defaultQualityOptions = (): LinkDownloadQualityOption[] => [
  {
    label: t("linkDownload.qualityBest"),
    quality: defaultQuality,
  },
];

const isTerminal = computed(() => {
  return (
    job.value?.status === "completed" ||
    job.value?.status === "failed" ||
    job.value?.status === "canceled"
  );
});

const progressText = computed(() => {
  if (!job.value) return "";
  if (job.value.bytesTotal > 0) {
    return `${Math.round(job.value.progress || 0)}%`;
  }
  return t(`linkDownload.status.${job.value.status}`);
});

onMounted(async () => {
  qualityOptions.value = defaultQualityOptions();

  try {
    linkSettings.value = await downloads.settings();
  } catch {
    linkSettings.value = null;
  }
});

onBeforeUnmount(() => {
  stopPolling();
  stopQualityOptionsTimer();
});

watch([() => linkForm.url, () => linkForm.downloader], () => {
  scheduleQualityOptionsLoad();
});

// TODO: this is a copy of the same function in FileListing.vue
const uploadInput = async (event: Event) => {
  const files = (event.currentTarget as HTMLInputElement)?.files;
  if (files === null) return;

  const folder_upload = !!files[0].webkitRelativePath;

  const uploadFiles: UploadList = [];
  for (let i = 0; i < files.length; i++) {
    const file = files[i];
    const fullPath = folder_upload ? file.webkitRelativePath : undefined;
    uploadFiles.push({
      file,
      name: file.name,
      size: file.size,
      isDir: false,
      fullPath,
    });
  }

  const path = route.path.endsWith("/") ? route.path : route.path + "/";

  const conflict = await upload.checkConflict(uploadFiles, path);

  if (conflict.length > 0) {
    layoutStore.showHover({
      prompt: "resolve-conflict",
      props: {
        conflict: conflict,
        isUploadAction: true,
      },
      confirm: (event: Event, result: Array<ConflictingResource>) => {
        event.preventDefault();
        layoutStore.closeHovers();
        for (let i = result.length - 1; i >= 0; i--) {
          const item = result[i];
          if (item.checked.length == 2) {
            continue;
          } else if (item.checked.length == 1 && item.checked[0] == "origin") {
            uploadFiles[item.index].overwrite = true;
          } else {
            uploadFiles.splice(item.index, 1);
          }
        }
        if (uploadFiles.length > 0) {
          upload.handleFiles(uploadFiles, path);
        }
      },
    });

    return;
  }

  upload.handleFiles(uploadFiles, path);
};

const openUpload = (isFolder: boolean) => {
  const input = document.createElement("input");
  input.type = "file";
  input.multiple = true;
  input.webkitdirectory = isFolder;
  // TODO: call the function in FileListing.vue instead
  input.onchange = uploadInput;
  input.click();
};

const uploadFile = () => {
  openUpload(false);
};
const uploadFolder = () => {
  openUpload(true);
};

const currentFolder = () => {
  const path = removePrefix(route.path);
  return path.endsWith("/") ? path : path + "/";
};

const openLinkDownload = async () => {
  if (!linkSettings.value) {
    linkSettings.value = await downloads.settings();
  }

  if (!linkSettings.value.enabled) return;

  const configuredDefaultQuality =
    linkSettings.value.defaultQuality || defaultQuality;
  linkForm.url = "";
  linkForm.path = linkSettings.value.defaultPath || currentFolder();
  linkForm.filename = "";
  linkForm.downloader = linkSettings.value.downloader || "auto";
  linkForm.overwrite = false;
  qualityOptions.value = defaultQualityOptions();
  qualityOptionsError.value = "";
  setQuality(configuredDefaultQuality);
  job.value = null;
  mode.value = "link";
  await nextTick();
  linkInput.value?.focus();
};

const setQuality = (quality: string) => {
  if (qualityOptions.value.some((option) => option.quality === quality)) {
    selectedQuality.value = quality;
    customQuality.value = "";
    return;
  }

  selectedQuality.value = "custom";
  customQuality.value = quality;
};

const getQuality = () => {
  if (selectedQuality.value === "custom") {
    return customQuality.value.trim();
  }
  return selectedQuality.value || defaultQuality;
};

const hasValidLink = () => {
  try {
    const parsed = new URL(linkForm.url);
    return parsed.protocol === "http:" || parsed.protocol === "https:";
  } catch {
    return false;
  }
};

const scheduleQualityOptionsLoad = () => {
  stopQualityOptionsTimer();
  qualityOptionsError.value = "";

  if (!hasValidLink()) {
    qualityOptions.value = defaultQualityOptions();
    setQuality(linkSettings.value?.defaultQuality || defaultQuality);
    return;
  }

  qualityFetchTimer.value = window.setTimeout(loadQualityOptions, 500);
};

const loadQualityOptions = async () => {
  if (!hasValidLink()) return;

  const previousQuality = getQuality();
  const serial = qualityFetchSerial.value + 1;
  qualityFetchSerial.value = serial;
  loadingQualityOptions.value = true;

  try {
    const response = await downloads.qualities(
      linkForm.url,
      linkForm.downloader
    );
    if (qualityFetchSerial.value !== serial) return;

    qualityOptions.value =
      response.options.length > 0 ? response.options : defaultQualityOptions();
    qualityOptionsError.value = response.error || "";
    if (
      qualityOptions.value.some((option) => option.quality === previousQuality)
    ) {
      selectedQuality.value = previousQuality;
      customQuality.value = "";
    } else if (selectedQuality.value !== "custom") {
      selectedQuality.value =
        qualityOptions.value[0]?.quality || defaultQuality;
      customQuality.value = "";
    }
  } catch (error: any) {
    if (qualityFetchSerial.value !== serial) return;
    qualityOptions.value = defaultQualityOptions();
    qualityOptionsError.value = error.message || String(error);
  } finally {
    if (qualityFetchSerial.value === serial) {
      loadingQualityOptions.value = false;
    }
  }
};

const startLinkDownload = async () => {
  submitting.value = true;
  stopPolling();
  job.value = null;

  try {
    if (
      selectedQuality.value === "custom" &&
      customQuality.value.trim() === ""
    ) {
      linkInput.value?.form?.reportValidity();
      return;
    }

    const created = await downloads.create({
      ...linkForm,
      filename: linkForm.filename || undefined,
      quality: getQuality(),
      path: linkForm.path || currentFolder(),
    });
    job.value = created;
    pollLinkDownload(created.id);
  } catch (error: any) {
    $showError(error);
  } finally {
    submitting.value = false;
  }
};

const pollLinkDownload = (id: string) => {
  stopPolling();

  const poll = async () => {
    try {
      const updated = await downloads.get(id);
      job.value = updated;
      if (isTerminal.value) {
        if (updated.status === "completed") {
          fileStore.reload = true;
          $showSuccess(t("linkDownload.completed"));
          layoutStore.closeHovers();
        }
        return;
      }
      pollTimer.value = window.setTimeout(poll, 1000);
    } catch (error: any) {
      $showError(error);
    }
  };

  pollTimer.value = window.setTimeout(poll, 500);
};

const cancelLinkDownload = async () => {
  if (!job.value) return;

  try {
    await downloads.cancel(job.value.id);
  } catch (error: any) {
    $showError(error);
  } finally {
    stopPolling();
    layoutStore.closeHovers();
  }
};

const stopPolling = () => {
  if (pollTimer.value !== null) {
    window.clearTimeout(pollTimer.value);
    pollTimer.value = null;
  }
};

const stopQualityOptionsTimer = () => {
  if (qualityFetchTimer.value !== null) {
    window.clearTimeout(qualityFetchTimer.value);
    qualityFetchTimer.value = null;
  }
};
</script>

<style scoped>
.upload-link-card {
  max-width: 34em;
  border: 1px solid var(--borderPrimary);
}

.upload-link-card form {
  border-top: 1px solid var(--borderPrimary);
}

.link-download p {
  margin: 0 0 0.9em;
}

.link-download {
  padding: 1em 1.25em 0.25em;
}

.upload-link-card .card-action {
  padding: 0 1.25em 1.25em;
}

.link-download-progress {
  margin-top: 1em;
}
</style>
