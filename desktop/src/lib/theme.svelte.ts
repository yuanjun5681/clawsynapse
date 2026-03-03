export type ThemeName = 'tron' | 'ares' | 'clu' | 'athena' | 'aphrodite' | 'poseidon';
export type Intensity = 'off' | 'light' | 'medium' | 'heavy';

export const THEMES: Record<ThemeName, { label: string; color: string }> = {
  tron:      { label: 'TRON',      color: '#00D4FF' },
  ares:      { label: 'ARES',      color: '#FF3333' },
  clu:       { label: 'CLU',       color: '#FF6600' },
  athena:    { label: 'ATHENA',    color: '#FFD700' },
  aphrodite: { label: 'APHRODITE', color: '#FF1493' },
  poseidon:  { label: 'POSEIDON',  color: '#0066FF' },
};

export const THEME_NAMES = Object.keys(THEMES) as ThemeName[];
export const INTENSITIES: Intensity[] = ['off', 'light', 'medium', 'heavy'];

const STORAGE_KEY_THEME = 'nanoclaw-theme';
const STORAGE_KEY_INTENSITY = 'nanoclaw-intensity';

function loadFromStorage<T>(key: string, fallback: T, valid: T[]): T {
  try {
    const v = localStorage.getItem(key);
    if (v && valid.includes(v as T)) return v as T;
  } catch { /* ignore */ }
  return fallback;
}

let _theme = $state<ThemeName>(loadFromStorage(STORAGE_KEY_THEME, 'clu', THEME_NAMES));
let _intensity = $state<Intensity>(loadFromStorage(STORAGE_KEY_INTENSITY, 'medium', INTENSITIES));

function applyToDOM() {
  document.documentElement.setAttribute('data-theme', _theme);
  document.documentElement.setAttribute('data-intensity', _intensity);
}

// Apply on load
applyToDOM();

export function getTheme(): ThemeName {
  return _theme;
}

export function getIntensity(): Intensity {
  return _intensity;
}

export function setTheme(t: ThemeName) {
  _theme = t;
  localStorage.setItem(STORAGE_KEY_THEME, t);
  applyToDOM();
}

export function setIntensity(i: Intensity) {
  _intensity = i;
  localStorage.setItem(STORAGE_KEY_INTENSITY, i);
  applyToDOM();
}
