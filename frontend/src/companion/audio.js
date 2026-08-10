import { noteForSession } from "./model.js";

const COMPLETION_PATTERNS = {
  chime: [[659.25, 0], [987.77, 0.11], [1318.51, 0.22]],
  bell: [[1046.5, 0], [1318.51, 0.13]],
  ding: [[1318.51, 0]],
  marimba: [[523.25, 0], [659.25, 0.12], [783.99, 0.24]],
  pop: [[783.99, 0], [523.25, 0.09]],
};

export class TonePlayer {
  constructor(AudioContextClass = globalThis.AudioContext || globalThis.webkitAudioContext) {
    this.AudioContextClass = AudioContextClass;
    this.context = null;
  }

  status() {
    return this.context?.state || "unavailable";
  }

  async resume() {
    if (!this.AudioContextClass) return false;
    if (!this.context) this.context = new this.AudioContextClass();
    if (this.context.state === "suspended") await this.context.resume();
    return this.context.state === "running";
  }

  pulse(sessionId, volume = 0.28, delay = 0) {
    return this.playFrequency(noteForSession(sessionId).frequency, volume, delay, 0.1);
  }

  completion(sound = "chime", volume = 0.28) {
    const pattern = COMPLETION_PATTERNS[sound] || COMPLETION_PATTERNS.chime;
    return pattern.map(([frequency, delay]) => this.playFrequency(frequency, volume, delay, 0.16));
  }

  playFrequency(frequency, volume, delay = 0, duration = 0.1) {
    if (!this.context || this.context.state !== "running") return false;
    const start = this.context.currentTime + Math.max(0, delay);
    const oscillator = this.context.createOscillator();
    const gain = this.context.createGain();
    oscillator.type = "sine";
    oscillator.frequency.setValueAtTime(frequency, start);
    gain.gain.setValueAtTime(0.0001, start);
    gain.gain.exponentialRampToValueAtTime(Math.max(0.0001, Math.min(1, volume)), start + 0.012);
    gain.gain.setValueAtTime(Math.max(0.0001, Math.min(1, volume)), start + Math.max(0.013, duration - 0.024));
    gain.gain.exponentialRampToValueAtTime(0.0001, start + duration);
    oscillator.connect(gain);
    gain.connect(this.context.destination);
    oscillator.start(start);
    oscillator.stop(start + duration + 0.01);
    return true;
  }
}
