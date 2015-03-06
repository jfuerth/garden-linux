#!/bin/bash

set -e

action=$1
container_path=$2
overlay_path=$2/overlay
rootfs_path=$2/rootfs
base_path=$3

function overlay_directory_in_rootfs() {
  # Skip if exists
  if [ ! -d $overlay_path/$1 ]
  then
    if [ -d $rootfs_path/$1 ]
    then
      cp -r $rootfs_path/$1 $overlay_path/
    else
      mkdir -p $overlay_path/$1
    fi
  fi

  mount -n --bind $overlay_path/$1 $rootfs_path/$1
  mount -n --bind -o remount,$2 $overlay_path/$1 $rootfs_path/$1
}

# Guide to variables in this script:
# $base_path - the original, shared rootfs_lucid64.               eg. /var/vcap/packages/rootfs_lucid64
# $rootfs_path - the container's read-only mirror of $base_path.  eg. /var/vcap/data/garden-linux/overlays/hi7rikbrget/rootfs
# $overlay_path - the container's read/write copy of $rootfs_path eg. /var/vcap/data/garden-linux/overlays/hi7rikbrget/overlay

function setup_fs_other() {
  mkdir -p $base_path/proc
  mkdir -p $base_path/run

#  mount -n --bind $base_path $rootfs_path
#  mount -n --bind -o remount,ro $base_path $rootfs_path
#
#  overlay_directory_in_rootfs /dev rw
#  overlay_directory_in_rootfs /etc rw
#  overlay_directory_in_rootfs /home rw
#  overlay_directory_in_rootfs /root rw
#  overlay_directory_in_rootfs /sbin rw
#  overlay_directory_in_rootfs /var rw
#
#  mkdir -p $overlay_path/run
#  overlay_directory_in_rootfs /run rw
#
#  mkdir -p $overlay_path/tmp
#  chmod 777 $overlay_path/tmp
#  overlay_directory_in_rootfs /tmp rw

   echo "FS copy command: rsync -ax $base_path/ $rootfs_path"
   rsync -ax $base_path/ $rootfs_path
}

function get_mountpoint() {
  df -P $1 | tail -1 | awk '{print $NF}'
}

function current_fs() {
  mountpoint=$(get_mountpoint $1)

  local mp
  local fs

  while read _ mp fs _; do
    if [ "$fs" = "rootfs" ]; then
      continue
    fi

    if [ "$mp" = "$mountpoint" ]; then
      echo $fs
    fi
  done < /proc/mounts
}

function should_use_overlayfs() {
  # load it so it's in /proc/filesystems
  modprobe -q overlayfs >/dev/null 2>&1 || true

  # cannot mount overlayfs in aufs
  if [ "$(current_fs $overlay_path)" == "aufs" ]; then
    return 1
  fi

  # cannot mount overlayfs in overlayfs; whiteout not supported
  if [ "$(current_fs $overlay_path)" == "overlayfs" ]; then
    return 1
  fi

  # check if it's a known filesystem
  grep -q overlayfs /proc/filesystems
}

function should_use_aufs() {
  # load it so it's in /proc/filesystems
  modprobe -q aufs >/dev/null 2>&1 || true

  # cannot mount aufs in aufs
  if [ "$(current_fs $overlay_path)" == "aufs" ]; then
    return 1
  fi

  # cannot mount aufs in overlayfs
  if [ "$(current_fs $overlay_path)" == "overlayfs" ]; then
    return 1
  fi

  # check if it's a known filesystem
  grep -q aufs /proc/filesystems
}

function setup_fs() {
  mkdir -p $overlay_path
  mkdir -p $rootfs_path

  if should_use_aufs; then
    mount -n -t aufs -o br:$overlay_path=rw:$base_path=ro+wh none $rootfs_path
  elif should_use_overlayfs; then
    mount -n -t overlayfs -o rw,upperdir=$overlay_path,lowerdir=$base_path none $rootfs_path
  else
    setup_fs_other
  fi
}

function rootfs_mountpoints() {
  cat /proc/mounts | grep $rootfs_path | awk '{print $2}'
}

function teardown_fs() {
  for i in $(seq 10); do

    if [ ! -z "$(rootfs_mountpoints)" ]; then
      umount $(rootfs_mountpoints) || true
    fi

    if [ -z "$(rootfs_mountpoints)" ]; then
      if rm -rf $container_path; then
        return 0
      fi
    fi

    sleep 0.5
  done

  echo "Failed to unmount all filesystems inside the container $container_path"
  return 1
}

if [ "$action" = "create" ]; then
  setup_fs
else
  teardown_fs
fi
