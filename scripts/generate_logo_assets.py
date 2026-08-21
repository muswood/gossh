#!/usr/bin/env python3
# owner: muswood | Email: mumu920@outlook.com
import io
import math
import os
import struct
import zlib


ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))


def clamp(value, lo=0, hi=255):
    return max(lo, min(hi, int(round(value))))


def mix(a, b, t):
    return tuple(clamp(a[i] + (b[i] - a[i]) * t) for i in range(4))


def alpha_over(dst, src):
    sa = src[3] / 255.0
    da = dst[3] / 255.0
    out_a = sa + da * (1 - sa)
    if out_a <= 0:
        return (0, 0, 0, 0)
    return tuple(
        clamp((src[i] * sa + dst[i] * da * (1 - sa)) / out_a)
        for i in range(3)
    ) + (clamp(out_a * 255),)


class Canvas:
    def __init__(self, w, h):
        self.w = w
        self.h = h
        self.px = bytearray(w * h * 4)

    def set_px(self, x, y, rgba):
        if 0 <= x < self.w and 0 <= y < self.h:
            i = (y * self.w + x) * 4
            dst = tuple(self.px[i:i + 4])
            self.px[i:i + 4] = bytes(alpha_over(dst, rgba))

    def fill_rect(self, x0, y0, x1, y1, color):
        for y in range(max(0, y0), min(self.h, y1)):
            for x in range(max(0, x0), min(self.w, x1)):
                self.set_px(x, y, color)

    def rounded_rect(self, x0, y0, x1, y1, r, color, outline=None, outline_width=0):
        for y in range(max(0, y0), min(self.h, y1)):
            for x in range(max(0, x0), min(self.w, x1)):
                dx = max(x0 + r - x, 0, x - (x1 - r - 1))
                dy = max(y0 + r - y, 0, y - (y1 - r - 1))
                dist = math.hypot(dx, dy)
                if dist <= r:
                    if outline and (x < x0 + outline_width or x >= x1 - outline_width or y < y0 + outline_width or y >= y1 - outline_width or dist > r - outline_width):
                        self.set_px(x, y, outline)
                    else:
                        self.set_px(x, y, color)

    def circle(self, cx, cy, r, color):
        rr = r * r
        for y in range(max(0, cy - r), min(self.h, cy + r + 1)):
            for x in range(max(0, cx - r), min(self.w, cx + r + 1)):
                if (x - cx) * (x - cx) + (y - cy) * (y - cy) <= rr:
                    self.set_px(x, y, color)

    def line(self, x0, y0, x1, y1, width, color):
        steps = max(abs(x1 - x0), abs(y1 - y0), 1)
        for i in range(steps + 1):
            t = i / steps
            x = int(round(x0 + (x1 - x0) * t))
            y = int(round(y0 + (y1 - y0) * t))
            self.circle(x, y, max(1, width // 2), color)

    def polygon(self, pts, color):
        min_y = max(0, min(p[1] for p in pts))
        max_y = min(self.h - 1, max(p[1] for p in pts))
        for y in range(min_y, max_y + 1):
            nodes = []
            j = len(pts) - 1
            for i in range(len(pts)):
                xi, yi = pts[i]
                xj, yj = pts[j]
                if (yi < y <= yj) or (yj < y <= yi):
                    nodes.append(int(xi + (y - yi) / (yj - yi) * (xj - xi)))
                j = i
            nodes.sort()
            for a, b in zip(nodes[0::2], nodes[1::2]):
                for x in range(max(0, a), min(self.w, b + 1)):
                    self.set_px(x, y, color)

    def star4(self, cx, cy, r1, r2, color):
        pts = [(cx, cy - r1), (cx + r2, cy - r2), (cx + r1, cy),
               (cx + r2, cy + r2), (cx, cy + r1), (cx - r2, cy + r2),
               (cx - r1, cy), (cx - r2, cy - r2)]
        self.polygon(pts, color)

    def downscale(self, factor):
        out = Canvas(self.w // factor, self.h // factor)
        for y in range(out.h):
            for x in range(out.w):
                acc = [0, 0, 0, 0]
                for yy in range(factor):
                    for xx in range(factor):
                        i = ((y * factor + yy) * self.w + (x * factor + xx)) * 4
                        for c in range(4):
                            acc[c] += self.px[i + c]
                n = factor * factor
                oi = (y * out.w + x) * 4
                out.px[oi:oi + 4] = bytes(clamp(v / n) for v in acc)
        return out

    def resize(self, width, height):
        if width == self.w and height == self.h:
            return self
        out = Canvas(width, height)
        for y in range(height):
            y0 = int(math.floor(y * self.h / height))
            y1 = int(math.ceil((y + 1) * self.h / height))
            for x in range(width):
                x0 = int(math.floor(x * self.w / width))
                x1 = int(math.ceil((x + 1) * self.w / width))
                acc = [0, 0, 0, 0]
                count = 0
                for sy in range(y0, min(self.h, y1)):
                    for sx in range(x0, min(self.w, x1)):
                        i = (sy * self.w + sx) * 4
                        for channel in range(4):
                            acc[channel] += self.px[i + channel]
                        count += 1
                oi = (y * width + x) * 4
                out.px[oi:oi + 4] = bytes(clamp(value / max(1, count)) for value in acc)
        return out


def write_png(path, canvas):
    raw = bytearray()
    for y in range(canvas.h):
        raw.append(0)
        start = y * canvas.w * 4
        raw.extend(canvas.px[start:start + canvas.w * 4])

    def chunk(kind, data):
        return (
            struct.pack(">I", len(data)) + kind + data +
            struct.pack(">I", zlib.crc32(kind + data) & 0xFFFFFFFF)
        )

    data = (
        b"\x89PNG\r\n\x1a\n" +
        chunk(b"IHDR", struct.pack(">IIBBBBB", canvas.w, canvas.h, 8, 6, 0, 0, 0)) +
        chunk(b"IDAT", zlib.compress(bytes(raw), 9)) +
        chunk(b"IEND", b"")
    )
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "wb") as f:
        f.write(data)
    return data


def render_logo(size):
    base_size = 1024
    scale = size / base_size
    s = size
    c = Canvas(s, s)

    for y in range(s):
        for x in range(s):
            t = (x + y) / (2 * s)
            base = mix((8, 12, 14, 255), (18, 38, 30, 255), t)
            c.set_px(x, y, base)

    def p(v):
        return int(round(v * scale))

    # App tile: graphite instead of the previous blue-heavy shape.
    c.rounded_rect(p(48), p(48), p(976), p(976), p(190), (12, 18, 20, 252), (45, 212, 151, 110), p(12))
    c.polygon([(p(48), p(720)), (p(48), p(976)), (p(976), p(976)), (p(976), p(620))], (11, 88, 70, 105))
    c.polygon([(p(700), p(48)), (p(976), p(48)), (p(976), p(340)), (p(820), p(250))], (245, 158, 11, 38))

    # Terminal card fills most of the icon, so it remains legible at 32-64px.
    c.rounded_rect(p(118), p(176), p(906), p(796), p(58), (5, 9, 11, 244), (94, 234, 212, 120), p(9))
    c.fill_rect(p(118), p(295), p(906), p(310), (34, 197, 94, 88))

    # Terminal header dots.
    c.circle(p(205), p(238), p(24), (248, 113, 113, 240))
    c.circle(p(278), p(238), p(24), (251, 191, 36, 240))
    c.circle(p(351), p(238), p(24), (34, 197, 94, 240))

    # Large prompt mark.
    c.line(p(232), p(430), p(352), p(512), p(48), (248, 250, 252, 255))
    c.line(p(352), p(512), p(232), p(594), p(48), (248, 250, 252, 255))
    c.line(p(420), p(618), p(590), p(618), p(44), (52, 211, 153, 255))

    # SSH network motif, larger and greener than the previous icon.
    c.line(p(560), p(488), p(720), p(410), p(22), (45, 212, 191, 230))
    c.line(p(560), p(488), p(744), p(570), p(22), (34, 197, 94, 210))
    c.line(p(720), p(410), p(744), p(570), p(14), (251, 191, 36, 150))
    for x, y, r, col in [
        (560, 488, 38, (34, 197, 94, 255)),
        (720, 410, 34, (45, 212, 191, 255)),
        (744, 570, 34, (251, 191, 36, 255)),
    ]:
        c.circle(p(x), p(y), p(r + 12), (5, 9, 11, 190))
        c.circle(p(x), p(y), p(r), col)

    # AI/automation hint.
    c.star4(p(795), p(245), p(40), p(14), (244, 114, 182, 250))
    c.star4(p(835), p(293), p(20), p(8), (253, 224, 71, 230))

    # Small "SSH" block wordmark.
    x0, y0, w, h, gap = p(246), p(710), p(38), p(10), p(14)
    for offset in [0, w + gap]:
        x = x0 + offset
        c.fill_rect(x, y0, x + w, y0 + h, (148, 163, 184, 230))
        c.fill_rect(x, y0 + p(24), x + w, y0 + p(24) + h, (148, 163, 184, 230))
        c.fill_rect(x, y0 + p(48), x + w, y0 + p(48) + h, (148, 163, 184, 230))
        c.fill_rect(x, y0, x + h, y0 + p(28), (148, 163, 184, 230))
        c.fill_rect(x + w - h, y0 + p(24), x + w, y0 + p(58), (148, 163, 184, 230))
    hx = x0 + p(2) * (w + gap)
    c.fill_rect(hx, y0, hx + h, y0 + p(58), (148, 163, 184, 230))
    c.fill_rect(hx + w - h, y0, hx + w, y0 + p(58), (148, 163, 184, 230))
    c.fill_rect(hx, y0 + p(24), hx + w, y0 + p(24) + h, (148, 163, 184, 230))

    return c


def make_ico(path, sizes):
    pngs = []
    for size in sizes:
        canvas = render_logo(size)
        bio = io.BytesIO()
        raw = bytearray()
        for y in range(canvas.h):
            raw.append(0)
            start = y * canvas.w * 4
            raw.extend(canvas.px[start:start + canvas.w * 4])

        def chunk(kind, data):
            return (
                struct.pack(">I", len(data)) + kind + data +
                struct.pack(">I", zlib.crc32(kind + data) & 0xFFFFFFFF)
            )

        bio.write(b"\x89PNG\r\n\x1a\n")
        bio.write(chunk(b"IHDR", struct.pack(">IIBBBBB", canvas.w, canvas.h, 8, 6, 0, 0, 0)))
        bio.write(chunk(b"IDAT", zlib.compress(bytes(raw), 9)))
        bio.write(chunk(b"IEND", b""))
        pngs.append((size, bio.getvalue()))

    offset = 6 + len(pngs) * 16
    out = bytearray(struct.pack("<HHH", 0, 1, len(pngs)))
    for size, data in pngs:
        dim = 0 if size >= 256 else size
        out.extend(struct.pack("<BBBBHHII", dim, dim, 0, 0, 1, 32, len(data), offset))
        offset += len(data)
    for _, data in pngs:
        out.extend(data)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "wb") as f:
        f.write(out)


def main():
    app_png = os.path.join(ROOT, "build", "appicon.png")
    frontend_png = os.path.join(ROOT, "frontend", "src", "assets", "images", "logo-universal.png")
    ico = os.path.join(ROOT, "build", "windows", "icon.ico")
    write_png(app_png, render_logo(1024))
    write_png(frontend_png, render_logo(1024))
    make_ico(ico, [16, 24, 32, 48, 64, 128, 256])
    print(app_png)
    print(frontend_png)
    print(ico)


if __name__ == "__main__":
    main()
