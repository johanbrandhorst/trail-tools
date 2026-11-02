import { LogoMark, Wordmark } from "@components/header/logo";
import SignInForm from "@components/header/sign-in-form";
import React from "react";
import { Link } from "react-router";

/**
 * SignInPage is shown instead of the pages that need a user: the app's mark
 * and tagline on one side, and the passkey sign in on the other.
 */
const SignInPage: React.FC = () => (
  <div className="flex min-h-full flex-col bg-stone-50 md:flex-row font-ui text-ink">
    <section className="relative flex min-h-80 flex-col justify-between gap-8 overflow-hidden bg-ink px-6 py-8 text-neutral-50 sm:px-12 sm:py-10 md:flex-1">
      <div className="flex items-center gap-3">
        <LogoMark className="h-[30px] w-[46px]" />
        <Wordmark className="text-[22px]" />
      </div>
      <div className="relative flex max-w-md flex-col gap-3 pb-20">
        <h1 className="text-3xl leading-tight font-bold sm:text-4xl">
          Built for the long way round.
        </h1>
        <p className="text-base leading-relaxed text-neutral-300">
          Training plans, race day plans and trip reports for trail runners.
        </p>
      </div>
      <svg
        aria-hidden="true"
        viewBox="0 0 600 120"
        preserveAspectRatio="none"
        className="absolute inset-x-0 bottom-0 h-[120px] w-full"
      >
        <path
          d="M0 100 L60 80 L100 90 L170 50 L210 65 L280 20 L330 55 L370 40 L430 75 L480 60 L540 90 L600 80"
          fill="none"
          className="stroke-ember"
          strokeWidth="2"
          strokeOpacity="0.55"
          strokeLinejoin="round"
        />
        <path
          d="M0 112 L80 98 L140 104 L220 82 L300 92 L380 74 L460 96 L540 88 L600 100"
          fill="none"
          stroke="#404040"
          strokeWidth="1.5"
          strokeLinejoin="round"
        />
      </svg>
    </section>
    <section className="flex flex-col items-center justify-center gap-6 px-6 py-10 md:flex-1">
      <SignInForm />
      <Link to="/privacy" className="text-sm text-stone-500 hover:underline">
        Privacy policy
      </Link>
    </section>
  </div>
);

export default SignInPage;
