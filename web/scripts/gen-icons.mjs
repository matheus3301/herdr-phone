// Dependency-free PNG icon generator (no runtime CDN, no native deps).
// Rasterizes the Herdr Phone field-instrument mark in the Deck/Bulkhead/Mist/
// Brass/Tide palette into the PNG sizes the manifest and iOS need. Run:
//   node scripts/gen-icons.mjs
import { deflateSync } from "node:zlib";
import { writeFileSync, mkdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const outDir = join(here, "..", "public", "icons");
mkdirSync(outDir, { recursive: true });

const C = {
  deck: [0x10, 0x18, 0x20],
  bulkhead: [0x19, 0x27, 0x32],
  seam: [0x0c, 0x14, 0x1b],
  mist: [0xdc, 0xe7, 0xe4],
  brass: [0xe3, 0xb3, 0x41],
  tide: [0x50, 0xa8, 0xa3],
  frame: [0x2b, 0x3d, 0x4a],
};

function crc32(buf) {
  let c = ~0;
  for (let i = 0; i < buf.length; i++) {
    c ^= buf[i];
    for (let k = 0; k < 8; k++) c = (c >>> 1) ^ (0xedb88320 & -(c & 1));
  }
  return ~c >>> 0;
}
function chunk(type, data) {
  const len = Buffer.alloc(4);
  len.writeUInt32BE(data.length, 0);
  const typeBuf = Buffer.from(type, "ascii");
  const body = Buffer.concat([typeBuf, data]);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(body), 0);
  return Buffer.concat([len, body, crc]);
}
function encodePng(size, px) {
  const sig = Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]);
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(size, 0);
  ihdr.writeUInt32BE(size, 4);
  ihdr[8] = 8; // bit depth
  ihdr[9] = 6; // RGBA
  const stride = size * 4;
  const raw = Buffer.alloc((stride + 1) * size);
  for (let y = 0; y < size; y++) {
    raw[y * (stride + 1)] = 0; // filter none
    px.copy(raw, y * (stride + 1) + 1, y * stride, y * stride + stride);
  }
  const idat = deflateSync(raw, { level: 9 });
  return Buffer.concat([
    sig,
    chunk("IHDR", ihdr),
    chunk("IDAT", idat),
    chunk("IEND", Buffer.alloc(0)),
  ]);
}

function draw(size, { pad }) {
  const px = Buffer.alloc(size * size * 4);
  const S = size;
  const set = (x, y, [r, g, b]) => {
    if (x < 0 || y < 0 || x >= S || y >= S) return;
    const i = (y * S + x) * 4;
    px[i] = r;
    px[i + 1] = g;
    px[i + 2] = b;
    px[i + 3] = 255;
  };
  const rect = (x0, y0, w, h, c) => {
    for (let y = Math.round(y0); y < Math.round(y0 + h); y++)
      for (let x = Math.round(x0); x < Math.round(x0 + w); x++) set(x, y, c);
  };
  const roundRect = (x0, y0, w, h, rad, c) => {
    for (let y = 0; y < h; y++)
      for (let x = 0; x < w; x++) {
        let inside = true;
        const corners = [
          [rad, rad],
          [w - rad, rad],
          [rad, h - rad],
          [w - rad, h - rad],
        ];
        if (x < rad && y < rad) inside = (x - rad) ** 2 + (y - rad) ** 2 <= rad * rad;
        else if (x > w - rad && y < rad) inside = (x - (w - rad)) ** 2 + (y - rad) ** 2 <= rad * rad;
        else if (x < rad && y > h - rad) inside = (x - rad) ** 2 + (y - (h - rad)) ** 2 <= rad * rad;
        else if (x > w - rad && y > h - rad)
          inside = (x - (w - rad)) ** 2 + (y - (h - rad)) ** 2 <= rad * rad;
        void corners;
        if (inside) set(Math.round(x0 + x), Math.round(y0 + y), c);
      }
  };
  const circle = (cx, cy, r, c) => {
    for (let y = -r; y <= r; y++)
      for (let x = -r; x <= r; x++) if (x * x + y * y <= r * r) set(cx + x, cy + y, c);
  };
  const line = (x1, y1, x2, y2, w, c) => {
    const steps = Math.max(Math.abs(x2 - x1), Math.abs(y2 - y1)) * 2 + 1;
    for (let t = 0; t <= steps; t++) {
      const x = x1 + ((x2 - x1) * t) / steps;
      const y = y1 + ((y2 - y1) * t) / steps;
      circle(Math.round(x), Math.round(y), Math.round(w / 2), c);
    }
  };

  // Background
  rect(0, 0, S, S, C.deck);
  // Field-instrument panel (safe zone respects pad for maskable)
  const p = pad * S;
  const px0 = p;
  const py0 = p;
  const pw = S - 2 * p;
  const rad = 0.055 * S;
  roundRect(px0, py0, pw, pw, rad, C.bulkhead);
  // Frame edge
  const inset = 0.008 * S;
  // Inset seams
  rect(px0 + 0.08 * pw, py0 + 0.22 * pw, pw * 0.84, inset * 1.4, C.seam);
  rect(px0 + 0.08 * pw, py0 + 0.78 * pw, pw * 0.84, inset * 1.4, C.seam);
  // Prompt caret (Mist)
  const cx = px0 + 0.2 * pw;
  const cy = py0 + 0.5 * pw;
  const cl = 0.11 * pw;
  line(cx, cy - cl, cx + cl, cy, 0.05 * pw, C.mist);
  line(cx + cl, cy, cx, cy + cl, 0.05 * pw, C.mist);
  // Command line (Tide)
  rect(px0 + 0.4 * pw, py0 + 0.47 * pw, pw * 0.26, pw * 0.06, C.tide);
  // Attention beacon (Brass)
  circle(Math.round(px0 + 0.78 * pw), Math.round(py0 + 0.5 * pw), Math.round(0.09 * pw), C.brass);

  return px;
}

const targets = [
  { file: "icon-192.png", size: 192, pad: 0.1875 },
  { file: "icon-512.png", size: 512, pad: 0.1875 },
  { file: "icon-maskable-512.png", size: 512, pad: 0.24 },
  { file: "apple-touch-icon.png", size: 180, pad: 0.14 },
];
for (const t of targets) {
  const png = encodePng(t.size, draw(t.size, { pad: t.pad }));
  writeFileSync(join(outDir, t.file), png);
  console.log("wrote", t.file, `${t.size}x${t.size}`, png.length, "bytes");
}
