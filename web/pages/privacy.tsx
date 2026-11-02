import { cardClass } from "@components/ui/styles";
import type { ReactNode } from "react";
import React from "react";
import { Link } from "react-router";

/**
 * The address people write to about their data. Update the policy's
 * effective date whenever its content changes.
 */
const CONTACT_EMAIL = "johan.brandhorst@gmail.com";
const EFFECTIVE_DATE = "November 2, 2026";

interface SectionProps {
  title: string;
  children: ReactNode;
}

const Section: React.FC<SectionProps> = ({ title, children }) => (
  <section className="flex flex-col gap-2">
    <h2 className="text-lg font-semibold text-gray-900">{title}</h2>
    {children}
  </section>
);

/**
 * PrivacyPage is the privacy policy. It's public, so people can read it
 * before they're invited, and services connected to the app, like Strava
 * and Garmin, can link to it.
 */
const PrivacyPage: React.FC = () => (
  <div className="flex justify-center py-4">
    <article
      className={`${cardClass} flex w-full max-w-3xl flex-col gap-6 p-6 text-sm leading-relaxed text-gray-700 sm:p-8`}
    >
      <header className="flex flex-col gap-1">
        <h1 className="text-2xl font-bold text-gray-900">Privacy policy</h1>
        <p className="text-gray-500">Effective {EFFECTIVE_DATE}</p>
      </header>

      <p>
        trailrunn.ing is a small, invite-only web app for planning trail
        running: training plans, workouts, routes, races and race plans, and
        trip and race reports. This policy explains what data it keeps about
        you, where that data comes from, and what you can do about it. In short:
        your data is used only to run the app for you. It isn&apos;t sold, used
        for advertising, or shared with anyone you haven&apos;t chosen to share
        it with.
      </p>

      <Section title="What we collect">
        <ul className="flex list-disc flex-col gap-1.5 pl-5">
          <li>
            <strong>Your account:</strong> your name, email address, handle,
            profile picture if you add one, and your passkeys&apos; public keys.
            We never see your passkeys&apos; private keys and don&apos;t use
            passwords.
          </li>
          <li>
            <strong>What you create:</strong> training plans, workouts and their
            logs, strength exercises and home equipment, routes, races, race
            plans, lactate tests, trip and race reports, and their photos.
          </li>
          <li>
            <strong>From Strava</strong>, if you connect it: your
            activities&apos; details, like their name, type, time, distance,
            elevation, heart rate and pace, and their recorded samples, like GPS
            positions.
          </li>
          <li>
            <strong>From Garmin Connect</strong>, if you connect it: your
            activities&apos; summaries and activity files, with the same kind of
            details and samples, and the sets of strength workouts recorded by
            your watch. With your permission, we also send the workouts you plan
            to your Garmin Connect calendar.
          </li>
          <li>
            <strong>Garmin LiveTrack links</strong> that you or your crew add to
            a race plan on race day, to follow your progress.
          </li>
          <li>
            <strong>Technical data:</strong> the server&apos;s logs, traces and
            error reports, which include IP addresses, the pages and API calls
            requested, and when.
          </li>
        </ul>
      </Section>

      <Section title="How we use it">
        <p>
          Only to provide the app&apos;s features to you: showing and analyzing
          your training, matching activities to planned workouts, suggesting
          routes from where you&apos;ve run before, forecasting weather for your
          workouts and races, and keeping the app secure and working. We
          don&apos;t profile you, show ads, or sell or rent your data.
        </p>
        <p>
          When you connect both Strava and Garmin, an activity recorded by your
          watch is kept once, with both sources linked, so neither overwrites
          the other.
        </p>
      </Section>

      <Section title="Who can see it">
        <p>
          Your data is private to you, except what you choose to share: a public
          profile and the trip and race reports you publish, and race plans and
          lactate tests you share by link. Privacy zones in your settings hide
          the parts of your routes near places like your home from what you
          share. Anyone with a share link can see what it shares until you turn
          it off.
        </p>
        <p>
          You can connect AI agents, like Claude or ChatGPT, through the
          app&apos;s MCP server. An agent you connect can read and change your
          data on your behalf, as you would, and the company providing it
          handles that data under its own privacy policy. You can revoke an
          agent&apos;s access at any time in your settings. The app itself
          doesn&apos;t send your data to AI or machine learning services.
        </p>
      </Section>

      <Section title="Service providers">
        <p>We use these companies to run the app:</p>
        <ul className="flex list-disc flex-col gap-1.5 pl-5">
          <li>
            <strong>Google Cloud</strong>, in the United States, runs the server
            and stores activity files, photos, logs, traces and error reports.
          </li>
          <li>
            <strong>Neon</strong> hosts the database.
          </li>
          <li>
            <strong>Open-Meteo</strong> receives the locations and times of
            workouts and races to forecast their weather, and{" "}
            <strong>OpenStreetMap&apos;s Nominatim</strong> receives place names
            you search for. Neither receives who you are.
          </li>
          <li>
            <strong>OpenStreetMap</strong> serves the map tiles your browser
            loads, so it sees your IP address and the areas you view.
          </li>
        </ul>
      </Section>

      <Section title="Cookies and storage on your device">
        <p>
          We use one cookie, to keep you signed in. The app also keeps unsaved
          drafts, like a strength workout in progress, and a copy of itself for
          offline use on your device. There are no analytics or tracking
          cookies.
        </p>
      </Section>

      <Section title="How long we keep it, and deleting it">
        <p>
          We keep your data while you have an account. Logs, traces and error
          reports are deleted after 30 days.
        </p>
        <p>
          You can delete what you&apos;ve created, and disconnect Strava or
          Garmin, at any time in the app. Disconnecting stops syncing and
          deletes our access tokens. Activities deleted on Strava or Garmin are
          deleted here too. To delete your whole account, or all the data from
          Strava or Garmin, email us and we&apos;ll delete it within 30 days,
          along with the files stored for it.
        </p>
      </Section>

      <Section title="Your rights">
        <p>
          You can ask us for a copy of your data, to correct it, or to delete
          it, wherever you live. Email us and we&apos;ll answer within 30 days.
        </p>
      </Section>

      <Section title="Security">
        <p>
          Data is encrypted in transit and at rest. Sign in is with passkeys
          only, and access tokens for Strava and Garmin are stored only on the
          server and never shown to the browser.
        </p>
      </Section>

      <Section title="Children">
        <p>The app isn&apos;t meant for anyone under 16.</p>
      </Section>

      <Section title="Changes">
        <p>
          We&apos;ll update this page, and its effective date, when this policy
          changes, and tell you in the app about changes that matter.
        </p>
      </Section>

      <Section title="Contact">
        <p>
          Write to{" "}
          <a
            href={`mailto:${CONTACT_EMAIL}`}
            className="font-medium text-brand-600 hover:underline"
          >
            {CONTACT_EMAIL}
          </a>{" "}
          with any questions about your data.
        </p>
      </Section>

      <footer className="border-t border-stone-200 pt-4 text-gray-500">
        <Link to="/" className="font-medium text-brand-600 hover:underline">
          Back to trailrunn.ing
        </Link>
      </footer>
    </article>
  </div>
);

export default PrivacyPage;
