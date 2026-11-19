/**
 * 窗口级拖拽落点规划。
 *
 * 有 file 类型的 DataTransferItem 时逐项用 `getAsFile()` 取文件、用
 * `webkitGetAsEntry().isDirectory` 识别目录；两者互不依赖下标，避免某些 WebKit
 * 省略目录伪 File 时按下标过滤误删合法文件。只有完全没有 file items 时才退回
 * `DataTransfer.files`。只使用标准 File/DataTransfer API，不读原生路径、不做
 * Blob/base64 转换、不递归目录，也不上传已识别的目录。
 */
/** Composer 已挂载（已登录收件箱）才会出现的原生桥接收门禁属性。 */
export const DESKTOP_DROP_READY_ATTR = 'data-flowdrop-drop-ready';
/** 原生桥交付真实 File[] 的事件名。 */
export const DESKTOP_FILES_EVENT = 'flowdrop:desktop-files';
/** 原生桥拖拽悬停状态事件名。 */
export const DESKTOP_DRAG_EVENT = 'flowdrop:desktop-drag';

export interface DropPlan {
  /** 待上传的普通文件，保持拖入顺序。 */
  files: File[];
  /** 被跳过的目录名，用于中文提示。 */
  directories: string[];
}

export function hasFilePayload(dataTransfer: DataTransfer | null | undefined): boolean {
  if (!dataTransfer) return false;
  return Array.from(dataTransfer.types ?? []).includes('Files');
}

/**
 * 校验原生桥 `flowdrop:desktop-files` 的 detail：只接受真实 File 与目录名字符串，
 * 不读路径、不做额外兼容。缺少/畸形的 detail 按空计划处理。
 */
export function planDesktopFiles(detail: unknown): DropPlan {
  const files: File[] = [];
  const directories: string[] = [];
  if (detail && typeof detail === 'object') {
    const { files: rawFiles, directories: rawDirs } = detail as {
      files?: unknown;
      directories?: unknown;
    };
    if (Array.isArray(rawFiles)) {
      for (const file of rawFiles) {
        if (file instanceof File) files.push(file);
      }
    }
    if (Array.isArray(rawDirs)) {
      for (const name of rawDirs) {
        if (typeof name === 'string' && name.length > 0) directories.push(name);
      }
    }
  }
  return { files, directories };
}

export function planDrop(dataTransfer: DataTransfer | null | undefined): DropPlan {
  const listed = dataTransfer?.files ? Array.from(dataTransfer.files) : [];
  const items = dataTransfer?.items ? Array.from(dataTransfer.items) : [];
  const fileItems = items.filter((item) => item.kind === 'file');
  if (fileItems.length === 0) {
    return { files: listed, directories: [] };
  }

  const files: File[] = [];
  const directories: string[] = [];
  for (const item of fileItems) {
    const entry = typeof item.webkitGetAsEntry === 'function' ? item.webkitGetAsEntry() : null;
    if (entry?.isDirectory) {
      directories.push(entry.name || '未命名文件夹');
      continue;
    }
    const file = typeof item.getAsFile === 'function' ? item.getAsFile() : null;
    // 有 file items 时以可得普通文件为准：已识别目录一律不上传，也不整体退回 listed。
    if (file) files.push(file);
  }
  return { files, directories };
}
