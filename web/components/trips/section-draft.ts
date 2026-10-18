import type { DistanceRange } from "@components/trips/sections";
import type { TripPhoto, TripReport, TripSection } from "gen/trips/v1/trips_pb";

// SectionInput is a section as saved with SetTripSections.
export interface SectionInput {
  id: string;
  title: string;
  body: string;
  photoIds: string[];
  startDistanceMeters?: number;
  endDistanceMeters?: number;
}

// Spliced returns a copy of items with count items at the index replaced.
const spliced = <Item>(
  items: Item[],
  index: number,
  count: number,
  ...inserted: Item[]
): Item[] => [
  ...items.slice(0, index),
  ...inserted,
  ...items.slice(index + count),
];

/**
 * DraftSection is a section of a story being edited. Edits are kept in the
 * browser until they're saved.
 */
export interface DraftSection {
  // Identifies the section in the editor, also before it's saved.
  key: string;
  // The ID of the saved section, empty for a new one.
  id: string;
  title: string;
  body: string;
  // The stretch of the track in meters, both or neither set when valid.
  start: number | undefined;
  end: number | undefined;
  photoIds: string[];
}

let nextKey = 0;

export const newSection = (
  fields: Partial<Omit<DraftSection, "key">> = {}
): DraftSection => {
  nextKey++;
  return {
    body: "",
    end: undefined,
    id: "",
    key: `new-${nextKey}`,
    photoIds: [],
    start: undefined,
    title: "",
    ...fields,
  };
};

// DraftSections returns the sections of a report as a draft.
export const draftSections = (sections: TripSection[]): DraftSection[] =>
  sections.map((section) => ({
    body: section.body,
    end: section.endDistanceMeters,
    id: section.id,
    key: section.id,
    photoIds: [...section.photoIds],
    start: section.startDistanceMeters,
    title: section.title,
  }));

// DraftRange returns the stretch of a section, if it has a valid one.
export const draftRange = (
  section: DraftSection
): DistanceRange | undefined => {
  if (
    section.start === undefined ||
    section.end === undefined ||
    !(section.end > section.start)
  ) {
    return undefined;
  }
  return { endMeters: section.end, startMeters: section.start };
};

// RangeError describes what's wrong with a section's stretch, if anything.
export const rangeError = (section: DraftSection): string | undefined => {
  if (section.start === undefined && section.end === undefined) {
    return undefined;
  }
  if (section.start === undefined || section.end === undefined) {
    return "Set both ends of the stretch, or neither.";
  }
  if (section.start < 0 || !(section.end > section.start)) {
    return "The stretch must end after it starts.";
  }
  return undefined;
};

/**
 * InsertionPoint returns where the photo at index among a saved section's
 * photos goes among a draft section's: after the nearest photo before it
 * that the draft section has, or else first.
 */
const insertionPoint = (
  photoIds: string[],
  saved: string[],
  index: number
): number => {
  let at = 0;
  for (const previous of saved.slice(0, index)) {
    const found = photoIds.indexOf(previous);
    if (found >= 0) {
      at = found + 1;
    }
  }
  return at;
};

/**
 * Reconcile fits a draft to the report's current photos: deleted photos are
 * dropped, and photos the draft doesn't have yet, e.g. just uploaded, go
 * where the server put them: after the photo before them, or first.
 */
export const reconcile = (
  sections: DraftSection[],
  report: TripReport
): DraftSection[] => {
  const existing = new Set(report.photos.map((photo) => photo.id));
  const listed = new Set<string>();
  const result = sections.map((section) => ({
    ...section,
    photoIds: section.photoIds.filter((id) => {
      if (!existing.has(id) || listed.has(id)) {
        return false;
      }
      listed.add(id);
      return true;
    }),
  }));
  for (const saved of report.sections) {
    for (const [index, id] of saved.photoIds.entries()) {
      if (!listed.has(id)) {
        const target =
          result.find((section) => section.id === saved.id) ?? result.at(-1);
        target?.photoIds.splice(
          insertionPoint(target.photoIds, saved.photoIds, index),
          0,
          id
        );
        listed.add(id);
      }
    }
  }
  return result;
};

// SameSections returns whether two drafts have the same content.
export const sameSections = (
  first: DraftSection[],
  second: DraftSection[]
): boolean =>
  first.length === second.length &&
  first.every((section, index) => {
    const other = second[index];
    return (
      other !== undefined &&
      section.id === other.id &&
      section.title.trim() === other.title.trim() &&
      section.body === other.body &&
      section.start === other.start &&
      section.end === other.end &&
      section.photoIds.join(",") === other.photoIds.join(",")
    );
  });

// SectionsToSave returns the draft as sections to save.
export const sectionsToSave = (sections: DraftSection[]): SectionInput[] =>
  sections.map((section) => {
    const result: SectionInput = {
      body: section.body,
      id: section.id,
      photoIds: section.photoIds,
      title: section.title.trim(),
    };
    const range = draftRange(section);
    if (range !== undefined) {
      result.startDistanceMeters = range.startMeters;
      result.endDistanceMeters = range.endMeters;
    }
    return result;
  });

// JoinText joins the text of two sections with a blank line.
const joinText = (first: string, second: string): string =>
  [first.trim(), second.trim()].filter((text) => text !== "").join("\n\n");

// Merge merges the section at the index with the next one.
export const mergeWithNext = (
  sections: DraftSection[],
  index: number
): DraftSection[] => {
  const first = sections[index];
  const second = sections[index + 1];
  if (first === undefined || second === undefined) {
    return sections;
  }
  const merged: DraftSection = {
    ...first,
    body: joinText(first.body, second.body),
    photoIds: [...first.photoIds, ...second.photoIds],
    title: first.title.trim() === "" ? second.title : first.title,
  };
  const firstRange = draftRange(first);
  const secondRange = draftRange(second);
  if (firstRange !== undefined && secondRange !== undefined) {
    merged.start = Math.min(firstRange.startMeters, secondRange.startMeters);
    merged.end = Math.max(firstRange.endMeters, secondRange.endMeters);
  } else if (firstRange === undefined) {
    merged.start = second.start;
    merged.end = second.end;
  }
  return spliced(sections, index, 2, merged);
};

/**
 * Remove removes the section at the index, moving its text and photos to the
 * section before it, or after it for the first section.
 */
export const removeSection = (
  sections: DraftSection[],
  index: number
): DraftSection[] => {
  const removed = sections[index];
  if (removed === undefined || sections.length < 2) {
    return sections;
  }
  const targetIndex = index === 0 ? 1 : index - 1;
  const target = sections[targetIndex];
  if (target === undefined) {
    return sections;
  }
  const updated: DraftSection =
    index === 0
      ? {
          ...target,
          body: joinText(removed.body, target.body),
          photoIds: [...removed.photoIds, ...target.photoIds],
        }
      : {
          ...target,
          body: joinText(target.body, removed.body),
          photoIds: [...target.photoIds, ...removed.photoIds],
        };
  return sections
    .map((section, position) => (position === targetIndex ? updated : section))
    .filter((section) => section !== removed);
};

// Move moves the section at the index by delta places.
export const moveSection = (
  sections: DraftSection[],
  index: number,
  delta: number
): DraftSection[] => {
  const target = index + delta;
  const moved = sections[index];
  if (moved === undefined || target < 0 || target >= sections.length) {
    return sections;
  }
  return spliced(spliced(sections, index, 1), target, 0, moved);
};

/**
 * Split splits the section at the index at a distance along the track. The
 * new section after it gets the rest of the stretch and the photos taken
 * there.
 */
export const splitSection = (
  sections: DraftSection[],
  index: number,
  distance: number,
  photos: Map<string, TripPhoto>
): DraftSection[] => {
  const section = sections[index];
  const range = section === undefined ? undefined : draftRange(section);
  if (
    section === undefined ||
    range === undefined ||
    distance <= range.startMeters ||
    distance >= range.endMeters
  ) {
    return sections;
  }
  const after = (id: string) => {
    const at = photos.get(id)?.trackDistanceMeters;
    return at !== undefined && at >= distance;
  };
  const first: DraftSection = {
    ...section,
    end: distance,
    photoIds: section.photoIds.filter((id) => !after(id)),
  };
  const second = newSection({
    end: range.endMeters,
    photoIds: section.photoIds.filter((id) => after(id)),
    start: distance,
  });
  return spliced(sections, index, 1, first, second);
};

// MovePhoto moves a photo to the end of another section.
export const movePhotoToSection = (
  sections: DraftSection[],
  photoId: string,
  key: string
): DraftSection[] =>
  sections.map((section) => {
    const without = section.photoIds.filter((id) => id !== photoId);
    return section.key === key
      ? { ...section, photoIds: [...without, photoId] }
      : { ...section, photoIds: without };
  });

/**
 * MovePhotoTo moves a photo to a place in a section, e.g. where it was
 * dropped: index is the place among the section's photos as they are, before
 * the photo moves.
 */
export const movePhotoTo = (
  sections: DraftSection[],
  photoId: string,
  key: string,
  index: number
): DraftSection[] => {
  const from =
    sections
      .find((section) => section.key === key)
      ?.photoIds.indexOf(photoId) ?? -1;
  // Moving down a section, the photo's old place is taken out first.
  const at = from >= 0 && from < index ? index - 1 : index;
  return sections.map((section) => {
    const without = section.photoIds.filter((id) => id !== photoId);
    return section.key === key
      ? { ...section, photoIds: spliced(without, at, 0, photoId) }
      : { ...section, photoIds: without };
  });
};

// MovePhotoWithin moves a photo by delta places within its section.
export const movePhotoWithin = (
  sections: DraftSection[],
  photoId: string,
  delta: number
): DraftSection[] =>
  sections.map((section) => {
    const index = section.photoIds.indexOf(photoId);
    const target = index + delta;
    if (index < 0 || target < 0 || target >= section.photoIds.length) {
      return section;
    }
    return {
      ...section,
      photoIds: spliced(
        spliced(section.photoIds, index, 1),
        target,
        0,
        photoId
      ),
    };
  });

/**
 * ApplySuggestion replaces the sections with suggested ones, keeping the
 * text: sections without a stretch, e.g. an introduction, stay first, and
 * the text of the others moves to the suggested section where they started.
 * The photos go where the suggestion puts them.
 */
export const applySuggestion = (
  sections: DraftSection[],
  suggested: TripSection[]
): DraftSection[] => {
  const kept: DraftSection[] = [];
  for (const section of sections) {
    if (draftRange(section) === undefined) {
      kept.push({ ...section, photoIds: [] });
    }
  }
  const next = suggested.map((section) =>
    newSection({
      end: section.endDistanceMeters,
      photoIds: [...section.photoIds],
      start: section.startDistanceMeters,
      title: section.title,
    })
  );
  for (const section of sections) {
    const range = draftRange(section);
    if (range === undefined || section.body.trim() === "") {
      continue;
    }
    const target =
      next.find((candidate) => {
        const candidateRange = draftRange(candidate);
        return (
          candidateRange !== undefined &&
          range.startMeters >= candidateRange.startMeters &&
          range.startMeters < candidateRange.endMeters
        );
      }) ?? next[0];
    if (target !== undefined) {
      target.body = joinText(target.body, section.body);
    }
  }
  return [...kept, ...next];
};
