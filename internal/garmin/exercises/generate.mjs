/**
 * Writes exercises.csv, Garmin's catalog of strength exercises, from the
 * profile of Garmin's FIT SDK, which the Training API's exercise names
 * match: the FIT SDK's camel case names in upper snake case, like
 * "SQUAT,GOBLET_SQUAT". Names starting with a digit start with an
 * underscore, like "_3_WAY_CALF_RAISE". Run it with the SDK installed:
 *
 *   npm install @garmin/fitsdk && node generate.mjs > exercises.csv
 */
import { Profile } from "@garmin/fitsdk";

const snake = (camel) => {
  const words = camel
    .replaceAll(/([a-z])([A-Z0-9])/g, "$1_$2")
    .replaceAll(/([0-9])([A-Za-z])/g, "$1_$2")
    .replaceAll(/([A-Z])([A-Z][a-z])/g, "$1_$2")
    .toUpperCase();
  return /^[0-9]/.test(words) ? `_${words}` : words;
};

const rows = ["category,name,fit_category,fit_name"];
for (const [categoryNumber, category] of Object.entries(Profile.types.exerciseCategory)) {
  const names = Profile.types[`${category}ExerciseName`];
  if (names === undefined) {
    continue;
  }
  for (const [nameNumber, name] of Object.entries(names)) {
    rows.push([snake(category), snake(name), categoryNumber, nameNumber].join(","));
  }
}
console.log(rows.join("\n"));
