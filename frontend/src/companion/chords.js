export const DEFAULT_BEEP_CHORD = "c-major";
export const BEEP_OCTAVE_ORDER = [4, 5, 3, 6];

const chord = (value, label, quality, pitchClasses, noteNames) => ({
  value,
  label,
  quality,
  pitchClasses,
  noteNames,
});

export const BEEP_CHORDS = [
  chord("c-major", "C Major", "major", [0, 4, 7], ["C", "E", "G"]),
  chord("db-major", "Db Major", "major", [1, 5, 8], ["Db", "F", "Ab"]),
  chord("d-major", "D Major", "major", [2, 6, 9], ["D", "F#", "A"]),
  chord("eb-major", "Eb Major", "major", [3, 7, 10], ["Eb", "G", "Bb"]),
  chord("e-major", "E Major", "major", [4, 8, 11], ["E", "G#", "B"]),
  chord("f-major", "F Major", "major", [5, 9, 0], ["F", "A", "C"]),
  chord("gb-major", "Gb Major", "major", [6, 10, 1], ["Gb", "Bb", "Db"]),
  chord("g-major", "G Major", "major", [7, 11, 2], ["G", "B", "D"]),
  chord("ab-major", "Ab Major", "major", [8, 0, 3], ["Ab", "C", "Eb"]),
  chord("a-major", "A Major", "major", [9, 1, 4], ["A", "C#", "E"]),
  chord("bb-major", "Bb Major", "major", [10, 2, 5], ["Bb", "D", "F"]),
  chord("b-major", "B Major", "major", [11, 3, 6], ["B", "D#", "F#"]),
  chord("c-minor", "C Minor", "minor", [0, 3, 7], ["C", "Eb", "G"]),
  chord("cs-minor", "C# Minor", "minor", [1, 4, 8], ["C#", "E", "G#"]),
  chord("d-minor", "D Minor", "minor", [2, 5, 9], ["D", "F", "A"]),
  chord("eb-minor", "Eb Minor", "minor", [3, 6, 10], ["Eb", "Gb", "Bb"]),
  chord("e-minor", "E Minor", "minor", [4, 7, 11], ["E", "G", "B"]),
  chord("f-minor", "F Minor", "minor", [5, 8, 0], ["F", "Ab", "C"]),
  chord("fs-minor", "F# Minor", "minor", [6, 9, 1], ["F#", "A", "C#"]),
  chord("g-minor", "G Minor", "minor", [7, 10, 2], ["G", "Bb", "D"]),
  chord("gs-minor", "G# Minor", "minor", [8, 11, 3], ["G#", "B", "D#"]),
  chord("a-minor", "A Minor", "minor", [9, 0, 4], ["A", "C", "E"]),
  chord("bb-minor", "Bb Minor", "minor", [10, 1, 5], ["Bb", "Db", "F"]),
  chord("b-minor", "B Minor", "minor", [11, 2, 6], ["B", "D", "F#"]),
];

const CHORD_BY_VALUE = new Map(BEEP_CHORDS.map((value) => [value.value, value]));

export function normalizeBeepChord(value) {
  return String(value || DEFAULT_BEEP_CHORD);
}

export function beepChord(value) {
  return CHORD_BY_VALUE.get(value) || CHORD_BY_VALUE.get(DEFAULT_BEEP_CHORD);
}

export function chordTonePool(value) {
  const selected = beepChord(value);
  return BEEP_OCTAVE_ORDER.flatMap((octave) => selected.pitchClasses.map((pitchClass, index) => {
    const midi = (octave + 1) * 12 + pitchClass;
    return {
      name: `${selected.noteNames[index]}${octave}`,
      frequency: 440 * (2 ** ((midi - 69) / 12)),
      midi,
    };
  }));
}

export function noteForToneSlot(slot, value = DEFAULT_BEEP_CHORD) {
  const pool = chordTonePool(value);
  const index = Math.max(0, Math.floor(Number(slot) || 0)) % pool.length;
  return pool[index];
}
