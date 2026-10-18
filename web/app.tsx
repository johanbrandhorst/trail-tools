import Header from "@components/header/header";
import Loading from "@components/loading";
import { Code } from "@connectrpc/connect";
import { useQuery } from "@connectrpc/connect-query";
import { getCurrentUser } from "gen/users/v1/users-UserService_connectquery";
import type { User } from "gen/users/v1/users_pb";
import ActivitiesPage from "pages/activities";
import ActivityPage from "pages/activity";
import CalendarPage from "pages/calendar";
import DashboardPage from "pages/dashboard";
import ForecastPage from "pages/forecast";
import InvitePage from "pages/invite";
import LactateTestPage from "pages/lactate-test";
import LactateTestRunPage from "pages/lactate-test-run";
import LactateTestsPage from "pages/lactate-tests";
import MapDataPage from "pages/map-data";
import OAuthConsentPage from "pages/oauth-consent";
import PlanPage from "pages/plan";
import PlansPage from "pages/plans";
import PublicProfilePage from "pages/public-profile";
import PublicRaceReportPage from "pages/public-race-report";
import PublicTripReportPage from "pages/public-trip-report";
import RacePlanPage from "pages/race-plan";
import RaceReportPage from "pages/race-report";
import RaceReportsPage from "pages/race-reports";
import RacesPage from "pages/races";
import RecordLactateTestPage from "pages/record-lactate-test";
import RoutePage from "pages/route";
import RoutesPage from "pages/routes";
import SettingsPage from "pages/settings";
import SharedRacePlanPage from "pages/shared-race-plan";
import SignInPage from "pages/sign-in";
import TripReportPage from "pages/trip-report";
import TripReportsPage from "pages/trip-reports";
import WorkoutPage from "pages/workout";
import React from "react";
import { Link, Outlet, Route, Routes } from "react-router";

// Layout is the header above the page.
const Layout: React.FC<{ user: User | null }> = ({ user }) => (
  <div className="flex min-h-full flex-col">
    <Header user={user} />
    <main className="flex grow justify-center bg-stone-50 print:bg-white">
      <div className="flex w-full max-w-7xl min-w-0 grow flex-col px-3 py-5 sm:px-4 print:px-0 print:py-0">
        <Outlet />
      </div>
    </main>
  </div>
);

const NotFound: React.FC = () => (
  <div className="flex grow flex-col items-center gap-2 py-10">
    <h1 className="text-xl font-semibold">Page not found</h1>
    <Link to="/" className="text-brand-600 hover:underline">
      Back to the dashboard
    </Link>
  </div>
);

const App: React.FC = () => {
  const { isPending, isError, data, error } = useQuery(
    getCurrentUser,
    {},
    {
      retry: (failureCount, err) => {
        // Disable automatic retries on Unauthenticated errors
        // Since we expect it if the user is not logged in.
        if (err.code === Code.Unauthenticated) {
          return false;
        }
        return failureCount < 3;
      },
      // 7 24 hour days
      staleTime: 1000 * 60 * 60 * 24 * 7,
    }
  );

  if (isPending) {
    return <Loading />;
  }

  // We got a response, let's check if it's an error
  // User is null if the user is not logged in.
  let user: User | null = null;
  if (isError) {
    if (error.code !== Code.Unauthenticated) {
      // Some other error, lets pretend the user is not logged in
      console.error("Failed to get current user:", error);
    }
    user = null;
  } else if (data.user === undefined) {
    // This is a programmer error, the user should always be set in the response.
    return (
      <div className="h-screen flex items-center justify-center">
        <h1>Error: No user data, please report this bug!</h1>
      </div>
    );
  } else {
    ({ user } = data);
  }

  /**
   * Pages that need a user are only routed to when signed in. Signed out,
   * any other page shows the sign in page instead.
   */
  return (
    <Routes>
      <Route element={<Layout user={user} />}>
        <Route path="/share/race-plans" element={<SharedRacePlanPage />} />
        <Route
          path="/u/:handle/races/:slug"
          element={<PublicRaceReportPage />}
        />
        <Route path="/u/:handle" element={<PublicProfilePage />} />
        <Route path="/u/:handle/:slug" element={<PublicTripReportPage />} />
        <Route path="/invite" element={<InvitePage />} />
        <Route
          path="/record/lactate-test"
          element={<RecordLactateTestPage />}
        />
        {user !== null && (
          <>
            <Route path="/" element={<DashboardPage user={user} />} />
            <Route path="/plans" element={<PlansPage />} />
            <Route path="/plans/:id" element={<PlanPage />} />
            <Route path="/workouts/:id" element={<WorkoutPage />} />
            <Route path="/routes" element={<RoutesPage />} />
            <Route path="/routes/new" element={<RoutePage isNew />} />
            <Route path="/routes/:id" element={<RoutePage isNew={false} />} />
            <Route path="/calendar" element={<CalendarPage />} />
            <Route path="/races" element={<RacesPage />} />
            <Route path="/race-plans/:id" element={<RacePlanPage />} />
            <Route path="/forecast" element={<ForecastPage />} />
            <Route path="/activities" element={<ActivitiesPage />} />
            <Route path="/activities/:id" element={<ActivityPage />} />
            <Route path="/trip-reports" element={<TripReportsPage />} />
            <Route path="/trip-reports/:id" element={<TripReportPage />} />
            <Route path="/race-reports" element={<RaceReportsPage />} />
            <Route path="/race-reports/:id" element={<RaceReportPage />} />
            <Route path="/lactate-tests" element={<LactateTestsPage />} />
            <Route path="/lactate-tests/new" element={<LactateTestRunPage />} />
            <Route path="/lactate-tests/:id" element={<LactateTestPage />} />
            <Route
              path="/lactate-tests/:id/run"
              element={<LactateTestRunPage />}
            />
            <Route path="/settings" element={<SettingsPage user={user} />} />
            <Route path="/map-data" element={<MapDataPage />} />
            <Route path="/oauth/authorize" element={<OAuthConsentPage />} />
            <Route path="*" element={<NotFound />} />
          </>
        )}
      </Route>
      {user === null && <Route path="*" element={<SignInPage />} />}
    </Routes>
  );
};

export default App;
