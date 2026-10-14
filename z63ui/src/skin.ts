// The body's two pictures and where their parts are, in each picture's own
// pixels. skin/make.sh makes them from the originals in skin/: the back from
// nik.png, same size, so its numbers were measured on nik.png at full size;
// the top from top.png with the lens taken off and trimmed, so its numbers
// were measured on the result. Replacing a picture means measuring again.
//
// A part with a key is a button the body MCU reads (hw/arm/nikon-z63-mcukeys.h
// names); one without is drawn but not wired in the machine, and says so.
import backUrl from "./assets/back.png";
import topUrl from "./assets/top.png";

export interface Hot {
  key?: string;
  x: number;
  y: number;
  r: number;
  title: string;
}

// The monitor's rectangle is where the live 1024 x 682 frame goes: the
// firmware's menus run edge to edge in the frame (tab column at x 0, title
// bar at y 0), and nik.png draws the same menu with its tab column at x 218,
// its title bar at y 450 and its right edge at about 870 -- a scale of
// 0.6475, which also puts the frame's bottom (892) where the drawn menu ends
// (about 895). Checked by laying a real menu frame over the drawn one at
// half opacity: icons, rows and the scroll bar fall within 2 px.
export const back = {
  image: backUrl,
  w: 1491,
  h: 1055,
  screen: { x: 218, y: 450, w: 663, h: 442 },
  buttons: [
    { key: "release-mode", x: 172, y: 308, r: 34, title: "Release mode / Protect (O)" },
    { key: "delete", x: 273, y: 307, r: 34, title: "Delete (Backspace)" },
    { key: "disp", x: 924, y: 288, r: 38, title: "DISP (D)" },
    { key: "af-on", x: 1135, y: 288, r: 40, title: "AF-ON (A)" },
    { key: "i", x: 1116, y: 544, r: 35, title: "i button (I)" },
    { key: "zoom-in", x: 1118, y: 845, r: 33, title: "Playback zoom in (+)" },
    { key: "menu", x: 1237, y: 848, r: 37, title: "MENU (M)" },
    { key: "zoom-out", x: 1118, y: 938, r: 33, title: "Playback zoom out / thumbnails / Help (-)" },
    { key: "play", x: 1237, y: 940, r: 36, title: "Playback (P)" },
    { key: "ok", x: 1178, y: 695, r: 33, title: "OK (Enter)" },
  ] as Hot[],
  ring: { x: 1178, y: 695, r: 92 },
  stick: { x: 1137, y: 425, r: 56 },
  dead: [
    { x: 995, y: 305, r: 24, title: "Photo/video selector -- not wired in the machine" },
    { x: 229, y: 192, r: 64, title: "Mode dial -- not wired in the machine" },
    { x: 1289, y: 222, r: 64, title: "Main command dial -- not wired in the machine" },
    { x: 815, y: 173, r: 24, title: "Diopter adjustment control" },
  ] as Hot[],
};

// The top, front edge up. The control panel's rectangle covers the panel
// the picture shows (its readout is the photographer's, not this machine's)
// with the one the machine's MCU draws.
export const top = {
  image: topUrl,
  w: 484,
  h: 297,
  panel: { x: 283, y: 119, w: 73, h: 72 },
  shutter: { x: 391, y: 44, r: 18, title: "Shutter-release button -- click: full press; Shift-click: half press (H, Space)" },
  buttons: [
    { key: "movie", x: 359, y: 76, r: 11, title: "Video-record button (R)" },
    { key: "iso", x: 392, y: 91, r: 11, title: "ISO sensitivity button" },
    { key: "exp-comp", x: 428, y: 93, r: 11, title: "Exposure compensation button" },
    { key: "illuminate", x: 377, y: 174, r: 10, title: "Illuminator button: lights the control panel" },
    { key: "disp", x: 306, y: 230, r: 12, title: "DISP (D)" },
    { key: "af-on", x: 372, y: 228, r: 12, title: "AF-ON (A)" },
    { key: "release-mode", x: 57, y: 222, r: 11, title: "Release mode / Protect (O)" },
    { key: "delete", x: 93, y: 228, r: 11, title: "Delete (Backspace)" },
  ] as Hot[],
  dead: [
    { x: 391, y: 44, r: 27, title: "Power switch -- the header's Power button starts and ends the run" },
    { x: 79, y: 156, r: 35, title: "Mode dial -- not wired in the machine" },
    { x: 426, y: 190, r: 31, title: "Main command dial -- not wired in the machine" },
  ] as Hot[],
};
