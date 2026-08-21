// owner: muswood | Email: mumu920@outlook.com
export function readFileAsDataURL(file: Blob) {
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result || ""));
    reader.onerror = () => reject(reader.error || new Error("读取图片失败"));
    reader.readAsDataURL(file);
  });
}

export async function compressTerminalBackgroundImage(file: File) {
  if (typeof createImageBitmap !== "function") return readFileAsDataURL(file);
  const bitmap = await createImageBitmap(file);
  try {
    const maxWidth = 1920;
    const maxHeight = 1080;
    const scale = Math.min(1, maxWidth / bitmap.width, maxHeight / bitmap.height);
    const width = Math.max(1, Math.round(bitmap.width * scale));
    const height = Math.max(1, Math.round(bitmap.height * scale));
    const canvas = document.createElement("canvas");
    canvas.width = width;
    canvas.height = height;
    const context = canvas.getContext("2d");
    if (!context) return readFileAsDataURL(file);
    context.drawImage(bitmap, 0, 0, width, height);
    const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, "image/jpeg", 0.84));
    return blob ? readFileAsDataURL(blob) : readFileAsDataURL(file);
  } finally {
    bitmap.close();
  }
}
