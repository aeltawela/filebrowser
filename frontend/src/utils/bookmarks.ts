import { users } from "@/api";
import { removePrefix } from "@/api/utils";
import { useAuthStore } from "@/stores/auth";
import { encodePath } from "@/utils/url";
import { computed, ref } from "vue";

export function normalizeBookmarkPath(value: string): string {
  let path = value || "/";

  if (path.startsWith("/files")) {
    path = removePrefix(path);
  }

  if (path === "") {
    path = "/";
  }

  if (!path.startsWith("/")) {
    path = `/${path}`;
  }

  const segments = path
    .split("/")
    .map(decodePathSegment)
    .filter((segment) => segment !== "" && segment !== "." && segment !== "..");

  return segments.length === 0 ? "/" : `/${segments.join("/")}`;
}

export function normalizeBookmark(bookmark: IBookmark): IBookmark {
  return {
    path: normalizeBookmarkPath(bookmark.path),
    isDir: bookmark.isDir,
  };
}

export function normalizeBookmarks(bookmarks: IBookmark[]): IBookmark[] {
  const seen = new Set<string>();
  const normalized: IBookmark[] = [];

  for (const bookmark of bookmarks) {
    const next = normalizeBookmark(bookmark);
    if (seen.has(next.path)) {
      continue;
    }
    seen.add(next.path);
    normalized.push(next);
  }

  return normalized;
}

export function bookmarkFromRoute(
  routePath: string,
  isDir: boolean
): IBookmark {
  return {
    path: normalizeBookmarkPath(routePath),
    isDir,
  };
}

export function bookmarkFromResource(resource: ResourceBase): IBookmark {
  return {
    path: normalizeBookmarkPath(resource.path),
    isDir: resource.isDir,
  };
}

export function bookmarkRoute(bookmark: IBookmark): string {
  const normalized = normalizeBookmark(bookmark);
  const route = `/files${encodePath(normalized.path)}`;

  if (normalized.isDir && normalized.path !== "/") {
    return `${route}/`;
  }

  if (normalized.path === "/") {
    return "/files/";
  }

  return route;
}

export function bookmarkDisplayName(
  bookmark: IBookmark,
  rootLabel = "/"
): string {
  const normalized = normalizeBookmark(bookmark);
  if (normalized.path === "/") {
    return rootLabel;
  }

  const parts = normalized.path.split("/").filter(Boolean);
  return parts[parts.length - 1] || rootLabel;
}

export function bookmarkIcon(bookmark: IBookmark): string {
  return bookmark.isDir ? "folder" : "insert_drive_file";
}

export function isBookmarked(
  bookmarks: IBookmark[] | undefined,
  bookmark: IBookmark
): boolean {
  const normalized = normalizeBookmark(bookmark);
  return (bookmarks ?? []).some(
    (current) => normalizeBookmarkPath(current.path) === normalized.path
  );
}

export function isBookmarkActive(
  bookmark: IBookmark,
  routePath: string
): boolean {
  return (
    normalizeBookmarkPath(bookmark.path) === normalizeBookmarkPath(routePath)
  );
}

export function addBookmarkToList(
  bookmarks: IBookmark[],
  bookmark: IBookmark
): IBookmark[] {
  const normalized = normalizeBookmark(bookmark);
  if (isBookmarked(bookmarks, normalized)) {
    return normalizeBookmarks(bookmarks);
  }
  return normalizeBookmarks([...bookmarks, normalized]);
}

export function removeBookmarkFromList(
  bookmarks: IBookmark[],
  bookmark: IBookmark
): IBookmark[] {
  const normalized = normalizeBookmark(bookmark);
  return normalizeBookmarks(bookmarks).filter(
    (current) => current.path !== normalized.path
  );
}

export function toggleBookmarkInList(
  bookmarks: IBookmark[],
  bookmark: IBookmark
): IBookmark[] {
  return isBookmarked(bookmarks, bookmark)
    ? removeBookmarkFromList(bookmarks, bookmark)
    : addBookmarkToList(bookmarks, bookmark);
}

export function useBookmarks() {
  const authStore = useAuthStore();
  const saving = ref(false);

  const bookmarks = computed(() => authStore.user?.bookmarks ?? []);

  const saveBookmarks = async (nextBookmarks: IBookmark[]) => {
    if (!authStore.user) {
      return;
    }

    const normalized = normalizeBookmarks(nextBookmarks);
    saving.value = true;
    try {
      await users.update({ id: authStore.user.id, bookmarks: normalized }, [
        "bookmarks",
      ]);
      authStore.updateUser({ bookmarks: normalized });
    } finally {
      saving.value = false;
    }
  };

  const addBookmark = (bookmark: IBookmark) =>
    saveBookmarks(addBookmarkToList(bookmarks.value, bookmark));

  const removeBookmark = (bookmark: IBookmark) =>
    saveBookmarks(removeBookmarkFromList(bookmarks.value, bookmark));

  const toggleBookmark = (bookmark: IBookmark) =>
    saveBookmarks(toggleBookmarkInList(bookmarks.value, bookmark));

  const hasBookmark = (bookmark: IBookmark) =>
    isBookmarked(bookmarks.value, bookmark);

  return {
    bookmarks,
    saving,
    addBookmark,
    removeBookmark,
    toggleBookmark,
    hasBookmark,
  };
}

function decodePathSegment(segment: string): string {
  try {
    return decodeURIComponent(segment);
  } catch {
    return segment;
  }
}
