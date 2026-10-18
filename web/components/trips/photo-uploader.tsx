import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { ConnectError } from "@connectrpc/connect";
import {
  ArrowUpTrayIcon,
  CheckCircleIcon,
  ExclamationTriangleIcon,
} from "@heroicons/react/24/outline";
import { preparePhoto } from "helpers/photos";
import type { DragEvent } from "react";
import React, { useRef, useState } from "react";

type UploadStatus = "waiting" | "preparing" | "uploading" | "done" | "failed";

interface UploadItem {
  key: string;
  name: string;
  status: UploadStatus;
  error: string;
}

const STATUS_LABELS: Record<UploadStatus, string> = {
  done: "Uploaded",
  failed: "Failed",
  preparing: "Preparing…",
  uploading: "Uploading…",
  waiting: "Waiting",
};

const errorMessage = (err: unknown): string => {
  if (err instanceof ConnectError) {
    return err.rawMessage;
  }
  if (err instanceof Error) {
    return err.message;
  }
  return "Upload failed";
};

/**
 * UploadFields are the fields of a request to upload a photo, prepared in
 * the browser, to a trip or race report.
 */
export interface UploadFields {
  caption: string;
  data: Uint8Array;
  location: { lat: number; lng: number } | undefined;
  takenLocalTime: string;
  takenTime: Timestamp | undefined;
}

interface PhotoUploaderProps {
  // Uploads a photo to the report.
  upload: (fields: UploadFields) => Promise<unknown>;
  // Called after each uploaded photo.
  onUploaded: () => Promise<void>;
}

let nextKey = 0;

/**
 * PhotoUploader uploads photos one at a time: each is read for its GPS
 * position and capture time and scaled down in the browser first.
 */
const PhotoUploader: React.FC<PhotoUploaderProps> = ({
  upload,
  onUploaded,
}) => {
  const inputRef = useRef<HTMLInputElement>(null);
  const [items, setItems] = useState<UploadItem[]>([]);
  const [dragging, setDragging] = useState(false);

  const setItem = (key: string, status: UploadStatus, error = "") => {
    setItems((current) =>
      current.map((item) =>
        item.key === key ? { ...item, error, status } : item
      )
    );
  };

  const uploadOne = async (item: UploadItem, file: File): Promise<void> => {
    try {
      setItem(item.key, "preparing");
      const prepared = await preparePhoto(file);
      setItem(item.key, "uploading");
      await upload({
        caption: "",
        data: prepared.data,
        location: prepared.location,
        takenLocalTime: prepared.takenLocalTime,
        takenTime:
          prepared.takenTime === undefined
            ? undefined
            : timestampFromDate(prepared.takenTime),
      });
      setItem(item.key, "done");
      await onUploaded();
    } catch (error) {
      setItem(item.key, "failed", errorMessage(error));
    }
  };

  const addFiles = (files: File[]) => {
    const images = files.filter(
      (file) => file.type.startsWith("image/") || /\.hei[cf]$/iu.test(file.name)
    );
    if (images.length === 0) {
      return;
    }
    const added = images.map((file) => {
      nextKey++;
      return {
        error: "",
        key: `upload-${nextKey}`,
        name: file.name,
        status: "waiting" as const,
      };
    });
    setItems((current) => [
      ...current.filter((item) => item.status !== "done"),
      ...added,
    ]);
    // Upload one at a time, to keep memory use low on phones.
    void added.reduce(async (previous, item, index) => {
      await previous;
      const file = images[index];
      if (file !== undefined) {
        await uploadOne(item, file);
      }
    }, Promise.resolve());
  };

  const handleDrop = (event: DragEvent) => {
    event.preventDefault();
    setDragging(false);
    addFiles([...event.dataTransfer.files]);
  };

  return (
    <div className="flex flex-col gap-3">
      <button
        type="button"
        className={`flex flex-col items-center gap-1 rounded-lg border-2 border-dashed px-4 py-6 text-center text-sm transition-colors ${
          dragging
            ? "border-brand-500 bg-brand-50 text-brand-700"
            : "border-gray-300 bg-gray-50 text-gray-600 hover:border-brand-400 hover:bg-brand-50/50"
        }`}
        onClick={() => {
          inputRef.current?.click();
        }}
        onDragOver={(event) => {
          event.preventDefault();
          setDragging(true);
        }}
        onDragLeave={() => {
          setDragging(false);
        }}
        onDrop={handleDrop}
      >
        <ArrowUpTrayIcon className="size-6" />
        <span className="font-medium">Add photos</span>
        <span className="text-xs text-gray-500">
          Click or drop photos here. Photos with a GPS position or taken during
          the activity are placed on the map.
        </span>
      </button>
      <input
        ref={inputRef}
        type="file"
        accept="image/*,.heic,.heif"
        multiple
        className="hidden"
        onChange={(event) => {
          addFiles([...(event.target.files ?? [])]);
          event.target.value = "";
        }}
      />
      {items.length > 0 && (
        <ul className="flex flex-col gap-1 text-sm">
          {items.map((item) => (
            <li key={item.key} className="flex items-center gap-2">
              {item.status === "done" && (
                <CheckCircleIcon className="size-4 shrink-0 text-emerald-600" />
              )}
              {item.status === "failed" && (
                <ExclamationTriangleIcon className="size-4 shrink-0 text-red-600" />
              )}
              <span className="min-w-0 truncate text-gray-700">
                {item.name}
              </span>
              <span
                className={`ml-auto shrink-0 text-xs ${item.status === "failed" ? "text-red-700" : "text-gray-500"}`}
              >
                {item.status === "failed"
                  ? item.error
                  : STATUS_LABELS[item.status]}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
};

export default PhotoUploader;
