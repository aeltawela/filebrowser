<template>
  <errors v-if="error" :errorCode="error.status" />
  <div class="row" v-else-if="!layoutStore.loading && settings !== null">
    <div class="column">
      <form class="card" @submit.prevent="save">
        <div class="card-title">
          <h2>{{ t("settings.globalSettings") }}</h2>
        </div>

        <div class="card-content">
          <p>
            <input type="checkbox" v-model="settings.signup" />
            {{ t("settings.allowSignup") }}
          </p>

          <p>
            <input type="checkbox" v-model="settings.createUserDir" />
            {{ t("settings.createUserDir") }}
          </p>

          <p>
            <input type="checkbox" v-model="settings.hideLoginButton" />
            {{ t("settings.hideLoginButton") }}
          </p>

          <p>
            <label class="small">{{ t("settings.userHomeBasePath") }}</label>
            <input
              class="input input--block"
              type="text"
              v-model="settings.userHomeBasePath"
            />
          </p>

          <p>
            <label for="minimumPasswordLength">{{
              t("settings.minimumPasswordLength")
            }}</label>
            <vue-number-input
              controls
              v-model.number="settings.minimumPasswordLength"
              id="minimumPasswordLength"
              :min="1"
            />
          </p>

          <h3>{{ t("settings.rules") }}</h3>
          <p class="small">{{ t("settings.globalRules") }}</p>
          <rules v-model:rules="settings.rules" />

          <div v-if="enableExec">
            <h3>{{ t("settings.executeOnShell") }}</h3>
            <p class="small">{{ t("settings.executeOnShellDescription") }}</p>
            <input
              class="input input--block"
              type="text"
              placeholder="bash -c, cmd /c, ..."
              v-model="shellValue"
            />
          </div>

          <h3>{{ t("settings.branding") }}</h3>

          <i18n-t
            keypath="settings.brandingHelp"
            tag="p"
            class="small"
            scope="global"
          >
            <a
              class="link"
              target="_blank"
              href="https://filebrowser.org/customization.html#custom-branding"
              >{{ t("settings.documentation") }}</a
            >
          </i18n-t>

          <p>
            <input
              type="checkbox"
              v-model="settings.branding.disableExternal"
              id="branding-links"
            />
            {{ t("settings.disableExternalLinks") }}
          </p>

          <p>
            <input
              type="checkbox"
              v-model="settings.branding.disableUsedPercentage"
              id="branding-used-disk"
            />
            {{ t("settings.disableUsedDiskPercentage") }}
          </p>

          <p>
            <label for="theme">{{ t("settings.themes.title") }}</label>
            <themes
              class="input input--block"
              v-model:theme="settings.branding.theme"
              id="theme"
            ></themes>
          </p>

          <p>
            <label for="branding-name">{{ t("settings.instanceName") }}</label>
            <input
              class="input input--block"
              type="text"
              v-model="settings.branding.name"
              id="branding-name"
            />
          </p>

          <p>
            <label for="branding-files">{{
              t("settings.brandingDirectoryPath")
            }}</label>
            <input
              class="input input--block"
              type="text"
              v-model="settings.branding.files"
              id="branding-files"
            />
          </p>

          <h3>{{ t("settings.tusUploads") }}</h3>

          <p class="small">{{ t("settings.tusUploadsHelp") }}</p>

          <div class="tusConditionalSettings">
            <p>
              <label for="tus-chunkSize">{{
                t("settings.tusUploadsChunkSize")
              }}</label>
              <input
                class="input input--block"
                type="text"
                v-model="formattedChunkSize"
                id="tus-chunkSize"
              />
            </p>

            <p>
              <label for="tus-retryCount">{{
                t("settings.tusUploadsRetryCount")
              }}</label>
              <vue-number-input
                controls
                v-model.number="settings.tus.retryCount"
                id="tus-retryCount"
                :min="0"
              />
            </p>
          </div>

          <h3>{{ t("settings.linkDownloads") }}</h3>
          <p class="small">{{ t("settings.linkDownloadsHelp") }}</p>

          <p>
            <input
              type="checkbox"
              v-model="settings.linkDownload.enabled"
              id="link-downloads-enabled"
            />
            {{ t("settings.linkDownloadsEnabled") }}
          </p>

          <p>
            <label for="link-downloads-default-path">{{
              t("settings.linkDownloadsDefaultPath")
            }}</label>
            <input
              class="input input--block"
              type="text"
              v-model="settings.linkDownload.defaultPath"
              id="link-downloads-default-path"
            />
          </p>

          <p>
            <label for="link-downloads-default-quality">{{
              t("settings.linkDownloadsDefaultQuality")
            }}</label>
            <select
              class="input input--block"
              v-model="defaultQualityPreset"
              id="link-downloads-default-quality"
            >
              <option value="bestvideo*+bestaudio/best">
                {{ t("linkDownload.qualityBest") }}
              </option>
              <option value="bv*[height<=1080]+ba/b[height<=1080]/wv*+ba/w">
                {{ t("linkDownload.quality1080") }}
              </option>
              <option value="bv*[height<=720]+ba/b[height<=720]/wv*+ba/w">
                {{ t("linkDownload.quality720") }}
              </option>
              <option value="bestaudio/best">
                {{ t("linkDownload.qualityAudio") }}
              </option>
              <option value="custom">
                {{ t("linkDownload.qualityCustom") }}
              </option>
            </select>
            <input
              v-if="defaultQualityPreset === 'custom'"
              class="input input--block"
              type="text"
              required
              :placeholder="t('linkDownload.formatSelectorPlaceholder')"
              v-model="settings.linkDownload.defaultQuality"
            />
            <span v-if="defaultQualityPreset === 'custom'" class="small">
              {{ t("linkDownload.formatSelectorHelp") }}
            </span>
          </p>

          <p>
            <label for="link-downloads-downloader">{{
              t("settings.linkDownloadsDownloader")
            }}</label>
            <select
              class="input input--block"
              v-model="settings.linkDownload.downloader"
              id="link-downloads-downloader"
            >
              <option value="auto">
                {{ t("linkDownload.downloaderAuto") }}
              </option>
              <option value="yt-dlp">
                {{ t("linkDownload.downloaderYTDLP") }}
              </option>
              <option value="direct">
                {{ t("linkDownload.downloaderDirect") }}
              </option>
            </select>
          </p>

          <p>
            <label for="link-downloads-ytdlp-path">{{
              t("settings.linkDownloadsYTDLPPath")
            }}</label>
            <input
              class="input input--block"
              type="text"
              v-model="settings.linkDownload.ytdlpPath"
              id="link-downloads-ytdlp-path"
            />
          </p>

          <div class="link-download-ytdlp-update">
            <button
              class="button button--flat"
              type="button"
              :disabled="updatingYTDLP"
              @click="updateYTDLP"
            >
              {{
                updatingYTDLP
                  ? t("settings.linkDownloadsYTDLPUpdating")
                  : t("settings.linkDownloadsYTDLPUpdate")
              }}
            </button>
            <span class="small">{{
              t("settings.linkDownloadsYTDLPUpdateHelp")
            }}</span>
          </div>
          <pre
            v-if="ytdlpUpdateOutput"
            class="small link-download-ytdlp-output"
            >{{ ytdlpUpdateOutput }}</pre
          >

          <h3>{{ t("settings.previews") }}</h3>
          <p>
            <input
              type="checkbox"
              v-model="settings.htmlPreview"
              id="html-preview-enabled"
            />
            {{ t("settings.htmlPreview") }}
          </p>
          <p class="small setting-warning">
            {{ t("settings.htmlPreviewWarning") }}
          </p>
        </div>

        <div class="card-action">
          <input
            class="button button--flat"
            type="submit"
            :value="t('buttons.update')"
          />
        </div>
      </form>
    </div>

    <div class="column">
      <form class="card" @submit.prevent="save">
        <div class="card-title">
          <h2>{{ t("settings.userDefaults") }}</h2>
        </div>

        <div class="card-content">
          <p class="small">{{ t("settings.defaultUserDescription") }}</p>

          <user-form
            :isNew="false"
            :isDefault="true"
            v-model:user="settings.defaults"
          />
        </div>

        <div class="card-action">
          <input
            class="button button--flat"
            type="submit"
            :value="t('buttons.update')"
          />
        </div>
      </form>
    </div>

    <div class="column">
      <form v-if="enableExec" class="card" @submit.prevent="save">
        <div class="card-title">
          <h2>{{ t("settings.commandRunner") }}</h2>
        </div>

        <div class="card-content">
          <i18n-t
            keypath="settings.commandRunnerHelp"
            tag="p"
            class="small"
            scope="global"
          >
            <code>FILE</code>
            <code>SCOPE</code>
            <a
              class="link"
              target="_blank"
              href="https://filebrowser.org/command-execution.html#hook-runner"
              >{{ t("settings.documentation") }}</a
            >
          </i18n-t>

          <div
            v-for="(command, key) in settings.commands"
            :key="key"
            class="collapsible"
          >
            <input :id="key" type="checkbox" />
            <label :for="key">
              <p>{{ capitalize(key) }}</p>
              <i class="material-icons">arrow_drop_down</i>
            </label>
            <div class="collapse">
              <textarea
                class="input input--block input--textarea"
                v-model.trim="commandObject[key]"
              ></textarea>
            </div>
          </div>
        </div>

        <div class="card-action">
          <input
            class="button button--flat"
            type="submit"
            :value="t('buttons.update')"
          />
        </div>
      </form>
    </div>
  </div>
</template>

<script setup lang="ts">
import { downloads, settings as api } from "@/api";
import { StatusError } from "@/api/utils";
import Rules from "@/components/settings/Rules.vue";
import Themes from "@/components/settings/Themes.vue";
import UserForm from "@/components/settings/UserForm.vue";
import { useLayoutStore } from "@/stores/layout";
import { enableExec, setHTMLPreview } from "@/utils/constants";
import { getTheme, setTheme } from "@/utils/theme";
import Errors from "@/views/Errors.vue";
import { computed, inject, onBeforeUnmount, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";

const error = ref<StatusError | null>(null);
const originalSettings = ref<ISettings | null>(null);
const settings = ref<ISettings | null>(null);
const debounceTimeout = ref<number | null>(null);
const updatingYTDLP = ref(false);
const ytdlpUpdateOutput = ref("");

const commandObject = ref<{
  [key: string]: string[] | string;
}>({});
const shellValue = ref<string>("");

const defaultLinkDownloadSettings: SettingsLinkDownload = {
  enabled: false,
  defaultPath: "",
  defaultQuality: "bestvideo*+bestaudio/best",
  downloader: "auto",
  ytdlpPath: "yt-dlp",
};

const defaultQualityPresetValues = new Set([
  "bestvideo*+bestaudio/best",
  "bv*[height<=1080]+ba/b[height<=1080]/wv*+ba/w",
  "bv*[height<=720]+ba/b[height<=720]/wv*+ba/w",
  "bestaudio/best",
]);

const $showError = inject<IToastError>("$showError")!;
const $showSuccess = inject<IToastSuccess>("$showSuccess")!;

const { t } = useI18n();

const layoutStore = useLayoutStore();
const defaultQualityCustomMode = ref(false);

const defaultQualityPreset = computed({
  get() {
    if (defaultQualityCustomMode.value) return "custom";

    const quality =
      settings.value?.linkDownload.defaultQuality ||
      "bestvideo*+bestaudio/best";
    return defaultQualityPresetValues.has(quality) ? quality : "custom";
  },
  set(value: string) {
    if (!settings.value) return;
    if (value === "custom") {
      defaultQualityCustomMode.value = true;
      return;
    }

    defaultQualityCustomMode.value = false;
    settings.value.linkDownload.defaultQuality = value;
  },
});

const formattedChunkSize = computed({
  get() {
    return settings?.value?.tus?.chunkSize
      ? formatBytes(settings?.value?.tus?.chunkSize)
      : "";
  },
  set(value: string) {
    // Use debouncing to allow the user to type freely without
    // interruption by the formatter
    // Clear the previous timeout if it exists
    if (debounceTimeout.value) {
      clearTimeout(debounceTimeout.value);
    }

    // Set a new timeout to apply the format after a short delay
    debounceTimeout.value = window.setTimeout(() => {
      if (settings.value) settings.value.tus.chunkSize = parseBytes(value);
    }, 1500);
  },
});

// Define funcs
const capitalize = (name: string, where: string | RegExp = "_") => {
  if (where === "caps") where = /(?=[A-Z])/;
  const split = name.split(where);
  name = "";

  for (let i = 0; i < split.length; i++) {
    name += split[i].charAt(0).toUpperCase() + split[i].slice(1) + " ";
  }

  return name.slice(0, -1);
};

const save = async () => {
  if (settings.value === null) return false;
  const newSettings: ISettings = {
    ...settings.value,
    shell:
      settings.value?.shell
        .join(" ")
        .trim()
        .split(" ")
        .filter((s: string) => s !== "") ?? [],
    commands: {},
  };

  const keys = Object.keys(settings.value.commands) as Array<
    keyof SettingsCommand
  >;
  for (const key of keys) {
    // not sure if we can safely assume non-null
    const newValue = commandObject.value[key];
    if (!newValue) continue;

    if (Array.isArray(newValue)) {
      newSettings.commands[key] = newValue;
    } else if (key in commandObject.value) {
      newSettings.commands[key] = newValue
        .split("\n")
        .filter((cmd: string) => cmd !== "");
    }
  }
  newSettings.shell = shellValue.value
    .trim()
    .split(" ")
    .filter((s) => s !== "");

  if (newSettings.branding.theme !== getTheme()) {
    setTheme(newSettings.branding.theme);
  }

  try {
    await api.update(newSettings);
    setHTMLPreview(newSettings.htmlPreview);
    $showSuccess(t("settings.settingsUpdated"));
    return true;
  } catch (e: any) {
    $showError(e);
    return false;
  }
};

const updateYTDLP = async () => {
  if (settings.value === null || updatingYTDLP.value) return;

  ytdlpUpdateOutput.value = "";
  updatingYTDLP.value = true;

  try {
    const saved = await save();
    if (!saved) return;

    const result = await downloads.updateYTDLP();
    ytdlpUpdateOutput.value =
      result.output ||
      (result.version
        ? t("settings.linkDownloadsYTDLPVersion", {
            version: result.version,
          })
        : t("settings.linkDownloadsYTDLPNoOutput"));
    $showSuccess(t("settings.linkDownloadsYTDLPUpdated"));
  } catch (e: any) {
    ytdlpUpdateOutput.value = e.message || String(e);
    $showError(e);
  } finally {
    updatingYTDLP.value = false;
  }
};
// Parse the user-friendly input (e.g., "20M" or "1T") to bytes
const parseBytes = (input: string) => {
  const regex = /^(\d+)(\.\d+)?(B|K|KB|M|MB|G|GB|T|TB)?$/i;
  const matches = input.match(regex);
  if (matches) {
    const size = parseFloat(matches[1].concat(matches[2] || ""));
    let unit: keyof SettingsUnit =
      matches[3].toUpperCase() as keyof SettingsUnit;
    if (!unit.endsWith("B")) {
      unit += "B";
    }
    const units: SettingsUnit = {
      KB: 1024,
      MB: 1024 ** 2,
      GB: 1024 ** 3,
      TB: 1024 ** 4,
    };
    return size * (units[unit as keyof SettingsUnit] || 1);
  } else {
    return 1024 ** 2;
  }
};
// Format the chunk size in bytes to user-friendly format
const formatBytes = (bytes: number) => {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let size = bytes;
  let unitIndex = 0;
  while (size >= 1024 && unitIndex < units.length - 1) {
    size /= 1024;
    unitIndex++;
  }
  return `${size}${units[unitIndex]}`;
};

// Define Hooks

onMounted(async () => {
  try {
    layoutStore.loading = true;
    const original: ISettings = await api.get();
    const newSettings: ISettings = {
      ...original,
      htmlPreview: original.htmlPreview ?? false,
      linkDownload: {
        ...defaultLinkDownloadSettings,
        ...original.linkDownload,
      },
      commands: {},
    };

    const keys = Object.keys(original.commands) as Array<keyof SettingsCommand>;
    for (const key of keys) {
      newSettings.commands[key] = original.commands[key];
      commandObject.value[key] = original.commands[key]!.join("\n");
    }

    originalSettings.value = original;
    settings.value = newSettings;
    shellValue.value = newSettings.shell.join(" ");
  } catch (err) {
    if (err instanceof Error) {
      error.value = err;
    }
  } finally {
    layoutStore.loading = false;
  }
});

// Clear the debounce timeout when the component is destroyed
onBeforeUnmount(() => {
  if (debounceTimeout.value) {
    clearTimeout(debounceTimeout.value);
  }
});
</script>

<style scoped>
.link-download-ytdlp-update {
  display: grid;
  gap: 0.5em;
  margin: 0 0 0.9em;
}

.link-download-ytdlp-update .button {
  justify-self: start;
}

.link-download-ytdlp-output {
  max-height: 12em;
  overflow: auto;
  white-space: pre-wrap;
}

.setting-warning {
  border-left: 3px solid var(--red);
  padding-left: 0.75em;
}
</style>
