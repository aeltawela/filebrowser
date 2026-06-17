<template>
  <div v-show="active" @click="closeHovers" class="overlay"></div>
  <nav :class="{ active }">
    <template v-if="isLoggedIn">
      <button @click="toAccountSettings" class="action">
        <i class="material-icons">person</i>
        <span>{{ user.username }}</span>
      </button>
      <button
        class="action"
        @click="toRoot"
        :aria-label="$t('sidebar.myFiles')"
        :title="$t('sidebar.myFiles')"
      >
        <i class="material-icons">folder</i>
        <span>{{ $t("sidebar.myFiles") }}</span>
      </button>

      <div v-if="user.perm.create">
        <button
          @click="showHover('newDir')"
          class="action"
          :aria-label="$t('sidebar.newFolder')"
          :title="$t('sidebar.newFolder')"
        >
          <i class="material-icons">create_new_folder</i>
          <span>{{ $t("sidebar.newFolder") }}</span>
        </button>

        <button
          @click="showHover('newFile')"
          class="action"
          :aria-label="$t('sidebar.newFile')"
          :title="$t('sidebar.newFile')"
        >
          <i class="material-icons">note_add</i>
          <span>{{ $t("sidebar.newFile") }}</span>
        </button>
      </div>

      <div v-if="user.bookmarks && user.bookmarks.length > 0">
        <p class="sidebar-section-title">{{ $t("sidebar.bookmarks") }}</p>
        <div
          v-for="bookmark in user.bookmarks"
          :key="bookmark.path"
          class="bookmark-row"
          :class="{ active: isBookmarkActive(bookmark) }"
        >
          <button
            class="action bookmark-action"
            @click="toBookmark(bookmark)"
            :aria-label="bookmarkDisplayName(bookmark)"
            :title="bookmark.path"
          >
            <i class="material-icons">{{ bookmarkIcon(bookmark) }}</i>
            <span class="bookmark-info">
              <span class="bookmark-name">{{
                bookmarkDisplayName(bookmark)
              }}</span>
              <span class="bookmark-path">{{ bookmark.path }}</span>
            </span>
          </button>
          <button
            class="action bookmark-remove-action"
            :aria-label="$t('buttons.removeBookmark')"
            :title="$t('buttons.removeBookmark')"
            @click="removeBookmark(bookmark)"
          >
            <i class="material-icons">close</i>
          </button>
        </div>
      </div>

      <div v-if="user.perm.admin">
        <button
          class="action"
          @click="toGlobalSettings"
          :aria-label="$t('sidebar.settings')"
          :title="$t('sidebar.settings')"
        >
          <i class="material-icons">settings_applications</i>
          <span>{{ $t("sidebar.settings") }}</span>
        </button>
      </div>
      <button
        v-if="canLogout"
        @click="logout"
        class="action"
        id="logout"
        :aria-label="$t('sidebar.logout')"
        :title="$t('sidebar.logout')"
      >
        <i class="material-icons">exit_to_app</i>
        <span>{{ $t("sidebar.logout") }}</span>
      </button>
    </template>
    <template v-else>
      <router-link
        v-if="!hideLoginButton"
        class="action"
        to="/login"
        :aria-label="$t('sidebar.login')"
        :title="$t('sidebar.login')"
      >
        <i class="material-icons">exit_to_app</i>
        <span>{{ $t("sidebar.login") }}</span>
      </router-link>

      <router-link
        v-if="signup"
        class="action"
        to="/login"
        :aria-label="$t('sidebar.signup')"
        :title="$t('sidebar.signup')"
      >
        <i class="material-icons">person_add</i>
        <span>{{ $t("sidebar.signup") }}</span>
      </router-link>
    </template>

    <div
      class="credits"
      v-if="isFiles && !disableUsedPercentage"
      style="width: 90%; margin: 2em 2.5em 3em 2.5em"
    >
      <progress-bar :val="usage.usedPercentage" size="small"></progress-bar>
      <br />
      {{ $t("sidebar.diskUsed", { used: usage.used, total: usage.total }) }}
    </div>

    <p class="credits">
      <span>
        <span v-if="disableExternal">File Browser</span>
        <a
          v-else
          rel="noopener noreferrer"
          target="_blank"
          href="https://github.com/filebrowser/filebrowser"
          >File Browser</a
        >
        <span> {{ " " }} {{ version }}</span>
      </span>
      <span>
        <a @click="help">{{ $t("sidebar.help") }}</a>
      </span>
    </p>
  </nav>
</template>

<script>
import { reactive } from "vue";
import { mapActions, mapState } from "pinia";
import { useAuthStore } from "@/stores/auth";
import { useFileStore } from "@/stores/file";
import { useLayoutStore } from "@/stores/layout";

import * as auth from "@/utils/auth";
import {
  version,
  signup,
  hideLoginButton,
  disableExternal,
  disableUsedPercentage,
  noAuth,
  logoutPage,
  loginPage,
} from "@/utils/constants";
import { files as api, users } from "@/api";
import {
  bookmarkDisplayName,
  bookmarkIcon,
  bookmarkRoute,
  isBookmarkActive as bookmarkIsActive,
} from "@/utils/bookmarks";
import ProgressBar from "@/components/ProgressBar.vue";
import prettyBytes from "pretty-bytes";

const USAGE_DEFAULT = { used: "0 B", total: "0 B", usedPercentage: 0 };

export default {
  name: "sidebar",
  setup() {
    const usage = reactive(USAGE_DEFAULT);
    return { usage, usageAbortController: new AbortController() };
  },
  components: {
    ProgressBar,
  },
  inject: ["$showError"],
  computed: {
    ...mapState(useAuthStore, ["user", "isLoggedIn"]),
    ...mapState(useFileStore, ["isFiles", "reload"]),
    ...mapState(useLayoutStore, ["currentPromptName"]),
    active() {
      return this.currentPromptName === "sidebar";
    },
    signup: () => signup,
    hideLoginButton: () => hideLoginButton,
    version: () => version,
    disableExternal: () => disableExternal,
    disableUsedPercentage: () => disableUsedPercentage,
    canLogout: () => !noAuth && (loginPage || logoutPage !== "/login"),
  },
  methods: {
    ...mapActions(useLayoutStore, ["closeHovers", "showHover"]),
    abortOngoingFetchUsage() {
      this.usageAbortController.abort();
    },
    async fetchUsage() {
      const path = this.$route.path.endsWith("/")
        ? this.$route.path
        : this.$route.path + "/";
      let usageStats = USAGE_DEFAULT;
      if (this.disableUsedPercentage) {
        return Object.assign(this.usage, usageStats);
      }
      try {
        this.abortOngoingFetchUsage();
        this.usageAbortController = new AbortController();
        const usage = await api.usage(path, this.usageAbortController.signal);
        usageStats = {
          used: prettyBytes(usage.used, { binary: true }),
          total: prettyBytes(usage.total, { binary: true }),
          usedPercentage: Math.round((usage.used / usage.total) * 100),
        };
      } finally {
        return Object.assign(this.usage, usageStats);
      }
    },
    toRoot() {
      this.$router.push({ path: "/files" });
      this.closeHovers();
    },
    toBookmark(bookmark) {
      this.$router.push({ path: bookmarkRoute(bookmark) });
      this.closeHovers();
    },
    bookmarkDisplayName(bookmark) {
      return bookmarkDisplayName(bookmark, this.$t("sidebar.myFiles"));
    },
    bookmarkIcon,
    isBookmarkActive(bookmark) {
      return bookmarkIsActive(bookmark, this.$route.path);
    },
    async removeBookmark(bookmark) {
      const next = (this.user.bookmarks ?? []).filter(
        (current) => current.path !== bookmark.path
      );

      try {
        await users.update({ id: this.user.id, bookmarks: next }, [
          "bookmarks",
        ]);
        useAuthStore().updateUser({ bookmarks: next });
      } catch (e) {
        this.$showError(e);
      }
    },
    toAccountSettings() {
      this.$router.push({ path: "/settings/profile" });
      this.closeHovers();
    },
    toGlobalSettings() {
      this.$router.push({ path: "/settings/global" });
      this.closeHovers();
    },
    help() {
      this.showHover("help");
    },
    logout: auth.logout,
  },
  watch: {
    $route: {
      handler(to) {
        if (to.path.includes("/files")) {
          this.fetchUsage();
        }
      },
      immediate: true,
    },
  },
  unmounted() {
    this.abortOngoingFetchUsage();
  },
};
</script>

<style scoped>
.sidebar-section-title {
  color: var(--textSecondary);
  font-size: 0.8em;
  font-weight: bold;
  margin: 1em 0 0.25em;
  padding: 0 2.5em;
  text-transform: uppercase;
}

.bookmark-row {
  align-items: center;
  display: flex;
}

.bookmark-row.active {
  background: var(--hover);
}

.bookmark-action {
  align-items: center;
  display: flex;
  flex: 1 1 auto;
  gap: 0.25em;
  min-width: 0;
}

.bookmark-row.active .bookmark-action,
.bookmark-row.active .bookmark-remove-action {
  background: transparent;
}

.bookmark-info {
  display: flex;
  flex: 1 1 auto;
  flex-direction: column;
  line-height: 1.2;
  min-width: 0;
  overflow: hidden;
}

.bookmark-name,
.bookmark-path {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.bookmark-path {
  color: var(--textSecondary);
  font-size: 0.75em;
}

.bookmark-remove-action {
  color: var(--textSecondary);
  flex: 0 0 auto;
  opacity: 0.7;
  width: auto;
}

.bookmark-remove-action:hover {
  opacity: 1;
}
</style>
