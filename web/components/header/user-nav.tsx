import Avatar from "@components/header/avatar";
import { Menu, MenuButton, MenuItem, MenuItems } from "@headlessui/react";
import type { User } from "gen/users/v1/users_pb";
import React from "react";
import { Link } from "react-router";

interface UserNavProps {
  user: User;
}

const UserNav: React.FC<UserNavProps> = ({ user }) => (
  <div className="ml-4 flex items-center pr-1.5 lg:ml-6">
    <Menu as="div" className="relative">
      {/* The ring with a gap is the brand's avatar style (avatar 4c). */}
      <MenuButton className="relative flex items-center rounded-full shadow-[0_0_0_3px_var(--color-ink),0_0_0_6px_var(--color-ember)] focus:outline-hidden data-[focus]:outline-2 data-[focus]:outline-offset-[9px] data-[focus]:outline-white">
        <span className="absolute -inset-1.5" />
        <span className="sr-only">Open user menu</span>
        <Avatar
          avatarUrl={user.avatarUrl}
          name={user.name}
          className="size-[34px] text-xs"
        />
      </MenuButton>
      <MenuItems
        transition
        className="absolute right-0 z-[1100] mt-3 w-56 font-ui origin-top-right rounded-md bg-white py-1 shadow-lg ring-1 ring-black/5 transition focus:outline-hidden data-[closed]:scale-95 data-[closed]:transform data-[closed]:opacity-0 data-[enter]:duration-100 data-[leave]:duration-75 data-[enter]:ease-out data-[leave]:ease-in"
      >
        <div className="border-b border-gray-100 px-4 py-2">
          <p className="truncate text-sm font-medium text-gray-900">
            {user.name}
          </p>
          {user.email !== "" && (
            <p className="truncate text-xs text-gray-500">{user.email}</p>
          )}
          {user.admin && (
            <p className="mt-1 inline-block rounded bg-amber-100 px-1.5 py-0.5 text-[10px] font-bold text-amber-900 uppercase">
              Admin
            </p>
          )}
        </div>
        <MenuItem key="settings">
          <Link
            to="/settings"
            className="block px-4 py-2 text-sm text-gray-700 data-[focus]:bg-gray-100 data-[focus]:outline-hidden"
          >
            Settings
          </Link>
        </MenuItem>
        {user.admin && (
          <MenuItem key="map-data">
            <Link
              to="/map-data"
              className="block px-4 py-2 text-sm text-gray-700 data-[focus]:bg-gray-100 data-[focus]:outline-hidden"
            >
              Map data
            </Link>
          </MenuItem>
        )}
        <MenuItem key="privacy">
          <Link
            to="/privacy"
            className="block px-4 py-2 text-sm text-gray-700 data-[focus]:bg-gray-100 data-[focus]:outline-hidden"
          >
            Privacy policy
          </Link>
        </MenuItem>
        <MenuItem key="logout">
          <a
            href="/logout"
            className="block px-4 py-2 text-sm text-gray-700 data-[focus]:bg-gray-100 data-[focus]:outline-hidden"
          >
            Sign out
          </a>
        </MenuItem>
      </MenuItems>
    </Menu>
  </div>
);

export default UserNav;
