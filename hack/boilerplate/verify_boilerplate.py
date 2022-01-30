#!/usr/bin/env python3

# Copyright 2015 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Verifies that all source files contain the necessary copyright boilerplate
# snippet.

import argparse
import datetime
import glob
import os
import re
import sys


def get_args():
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "filenames",
        help="list of files to check, all files if unspecified",
        nargs='*')

    rootdir = os.path.abspath('.')
    parser.add_argument("--rootdir",
                        default=rootdir,
                        help="root directory to examine")

    default_boilerplate_dir = os.path.join(rootdir, "verify/boilerplate")
    parser.add_argument("--boilerplate-dir", default=default_boilerplate_dir)

    parser.add_argument(
        '--skip',
        default=[
            'external/bazel_tools',
            '.git',
            'node_modules',
            '_output',
            'third_party',
            'vendor',
            'verify/boilerplate/test',
            'verify_boilerplate.py',
        ],
        action='append',
        help='Customize paths to avoid',
    )
    return parser.parse_args()


def get_refs():
    refs = {}

    template_dir = ARGS.boilerplate_dir
    if not os.path.isdir(template_dir):
        template_dir = os.path.dirname(template_dir)
    for path in glob.glob(os.path.join(template_dir, "boilerplate.*.txt")):
        extension = os.path.basename(path).split(".")[1]

        # Pass the encoding parameter to avoid ascii decode error for some
        # platform.
        ref_file = open(path, 'r', encoding='utf-8')
        ref = ref_file.read().splitlines()
        ref_file.close()
        refs[extension] = ref

    return refs

# given the file contents, return true if the file appears to be generated
def is_generated(data):
    if re.search(r"^// Code generated by .*\. DO NOT EDIT\.$", data, re.MULTILINE):
        return True
    return False


def file_passes(filename, refs, regexs):  # pylint: disable=too-many-locals
    try:
        # Pass the encoding parameter to avoid ascii decode error for some
        # platform.
        with open(filename, 'r', encoding='utf-8') as fp:
            file_data = fp.read()
    except IOError:
        return False

    if not file_data:
        return True  # Nothing to copyright in this empty file.

    basename = os.path.basename(filename)
    extension = file_extension(filename)
    ref = refs[basename]
    if extension != "":
        ref = refs[extension]

    ref = ref.copy()

    # remove build tags from the top of Go files
    lang_identifiers = {
        "go": "go_build_constraints",
        "sh": "shebang",
        "py": "shebang"
    }
    if extension in lang_identifiers:
        con = regexs[lang_identifiers["go_build_constraints"]]
        (file_data, found) = con.subn("", file_data, 1)

    data = file_data.splitlines()

    # if our test file is smaller than the reference it surely fails!
    if len(ref) > len(data):
        return False

    # trim our file to the same number of lines as the reference file
    data = data[:len(ref)]

    # check if we encounter a 'YEAR' placeholder if the file is generated
    if is_generated(file_data):
        for line in data:
            if "Copyright YEAR" in line:
                return False

    year = regexs["year"]
    for datum in data:
        if year.search(datum):
            return False

    # Replace all occurrences of the regex "2017|2016|2015|2014" with "YEAR"
    when = regexs["date"]
    for idx, datum in enumerate(data):
        (data[idx], found) = when.subn('YEAR', datum)
        if found != 0:
            break

    # if we don't match the reference at this point, fail
    return ref == data


def file_extension(filename):
    return os.path.splitext(filename)[1].split(".")[-1].lower()


# even when generated by bazel we will complain about some generated files
# not having the headers. since they're just generated, ignore them
IGNORE_HEADERS = ['// Code generated by go-bindata.']


def has_ignored_header(pathname):
    # Pass the encoding parameter to avoid ascii decode error for some
    # platform.
    with open(pathname, 'r', encoding='utf-8') as myfile:
        data = myfile.read()
        for header in IGNORE_HEADERS:
            if data.startswith(header):
                return True
    return False


def normalize_files(files):
    newfiles = []
    for pathname in files:
        if any(x in pathname for x in ARGS.skip):
            continue
        newfiles.append(pathname)
    for idx, pathname in enumerate(newfiles):
        if not os.path.isabs(pathname):
            newfiles[idx] = os.path.join(ARGS.rootdir, pathname)
    return newfiles


def get_files(extensions):
    files = []
    if ARGS.filenames:
        files = ARGS.filenames
    else:
        for root, dirs, walkfiles in os.walk(ARGS.rootdir):
            # don't visit certain dirs. This is just a performance improvement
            # as we would prune these later in normalize_files(). But doing it
            # cuts down the amount of filesystem walking we do and cuts down
            # the size of the file list
            for dpath in ARGS.skip:
                if dpath in dirs:
                    dirs.remove(dpath)

            for name in walkfiles:
                pathname = os.path.join(root, name)
                files.append(pathname)

    files = normalize_files(files)
    outfiles = []
    for pathname in files:
        basename = os.path.basename(pathname)
        extension = file_extension(pathname)
        if extension in extensions or basename in extensions:
            if not has_ignored_header(pathname):
                outfiles.append(pathname)
    return outfiles


def get_dates():
    years = datetime.datetime.now().year
    return '(%s)' % '|'.join((str(year) for year in range(2014, years + 1)))


def get_regexs():
    regexs = {}
    # Search for "YEAR" which exists in the boilerplate, but shouldn't in the real thing
    regexs["year"] = re.compile('YEAR')
    # dates can be any year between 2014 and the current year, company holder names can be anything
    regexs["date"] = re.compile(get_dates())
    # strip // +build \n\n build constraints
    regexs["go_build_constraints"] = re.compile(r"^(//( \+build|go:build).*\n)+\n",
                                                re.MULTILINE)
    # strip #!.* from shell/python scripts
    regexs["shebang"] = re.compile(r"^(#!.*\n)\n*", re.MULTILINE)
    return regexs


def nonconforming_lines(files):
    yield '%d files have incorrect boilerplate headers:' % len(files)
    for fp in files:
        yield os.path.relpath(fp, ARGS.rootdir)


def main():
    regexs = get_regexs()
    refs = get_refs()
    filenames = get_files(refs.keys())
    nonconforming_files = []
    for filename in sorted(filenames):
        if not file_passes(filename, refs, regexs):
            nonconforming_files.append(filename)

    if nonconforming_files:
        for line in nonconforming_lines(nonconforming_files):
            print(line)
        sys.exit(1)


if __name__ == "__main__":
    ARGS = get_args()
    main()
