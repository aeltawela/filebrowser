import { beforeEach, describe, expect, it, vi } from "vitest";
import { createPinia, setActivePinia } from "pinia";
import { users } from "@/api";
import { useAuthStore } from "@/stores/auth";
import {
  addBookmarkToList,
  bookmarkDisplayName,
  bookmarkFromRoute,
  bookmarkIcon,
  bookmarkRoute,
  normalizeBookmarkPath,
  removeBookmarkFromList,
  toggleBookmarkInList,
  useBookmarks,
} from "@/utils/bookmarks";

vi.mock("@/api", () => ({
  users: {
    update: vi.fn().mockResolvedValue(undefined),
  },
}));

vi.mock("@/api/utils", () => ({
  removePrefix: (value: string) => value.replace(/^\/files/, "") || "/",
}));

describe("bookmark path helpers", () => {
  it("normalizes route paths to decoded stored paths", () => {
    expect(normalizeBookmarkPath("/files/docs/")).toBe("/docs");
    expect(normalizeBookmarkPath("/files/My%20Folder/report%231.txt")).toBe(
      "/My Folder/report#1.txt"
    );
    expect(bookmarkFromRoute("/files/My%20Folder/", true)).toEqual({
      path: "/My Folder",
      isDir: true,
    });
  });

  it("creates encoded router paths for files and folders", () => {
    expect(bookmarkRoute({ path: "/", isDir: true })).toBe("/files/");
    expect(bookmarkRoute({ path: "/My Folder", isDir: true })).toBe(
      "/files/My%20Folder/"
    );
    expect(
      bookmarkRoute({ path: "/My Folder/report#1.txt", isDir: false })
    ).toBe("/files/My%20Folder/report%231.txt");
  });

  it("builds readable labels and icons", () => {
    expect(bookmarkDisplayName({ path: "/", isDir: true }, "My files")).toBe(
      "My files"
    );
    expect(
      bookmarkDisplayName({ path: "/Nested Folder/report.txt", isDir: false })
    ).toBe("report.txt");
    expect(bookmarkIcon({ path: "/Nested Folder", isDir: true })).toBe(
      "folder"
    );
    expect(
      bookmarkIcon({ path: "/Nested Folder/report.txt", isDir: false })
    ).toBe("insert_drive_file");
  });
});

describe("bookmark list helpers", () => {
  it("adds, dedupes, preserves order, and removes bookmarks", () => {
    const initial: IBookmark[] = [{ path: "/docs", isDir: true }];

    const added = addBookmarkToList(initial, {
      path: "/docs/readme.txt",
      isDir: false,
    });
    expect(added).toEqual([
      { path: "/docs", isDir: true },
      { path: "/docs/readme.txt", isDir: false },
    ]);

    expect(addBookmarkToList(added, { path: "/docs/", isDir: false })).toEqual(
      added
    );

    expect(
      removeBookmarkFromList(added, { path: "/docs", isDir: true })
    ).toEqual([{ path: "/docs/readme.txt", isDir: false }]);
  });

  it("toggles bookmarks", () => {
    const bookmark = { path: "/docs", isDir: true };
    expect(toggleBookmarkInList([], bookmark)).toEqual([bookmark]);
    expect(toggleBookmarkInList([bookmark], bookmark)).toEqual([]);
  });
});

describe("useBookmarks", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
  });

  it("persists bookmarks and updates the auth store after a successful API call", async () => {
    const authStore = useAuthStore();
    authStore.$patch({
      user: {
        id: 1,
        username: "alice",
        bookmarks: [],
      } as IUser,
    });

    const { toggleBookmark } = useBookmarks();
    await toggleBookmark({ path: "/docs/", isDir: true });

    const expected = [{ path: "/docs", isDir: true }];
    expect(users.update).toHaveBeenCalledWith({ id: 1, bookmarks: expected }, [
      "bookmarks",
    ]);
    expect(authStore.user?.bookmarks).toEqual(expected);
  });
});
