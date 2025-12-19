#!/usr/bin/env python3
"""
Setup script for the podcast index library.
"""

from setuptools import setup, find_packages

with open("README.md", "r", encoding="utf-8") as fh:
    long_description = fh.read()

setup(
    name="podcast-index",
    version="1.0.0",
    author="PodPing Team",
    author_email="team@podping.org",
    description="Podcast Index API client library",
    long_description=long_description,
    long_description_content_type="text/markdown",
    url="https://github.com/Podcastindex-org/podping",
    packages=find_packages(),
    classifiers=[
        "Development Status :: 4 - Beta",
        "Intended Audience :: Developers",
        "License :: OSI Approved :: MIT License",
        "Operating System :: OS Independent",
        "Programming Language :: Python :: 3",
        "Programming Language :: Python :: 3.8",
        "Programming Language :: Python :: 3.9",
        "Programming Language :: Python :: 3.10",
        "Programming Language :: Python :: 3.11",
        "Programming Language :: Python :: 3.12",
    ],
    python_requires=">=3.8",
    install_requires=[
        "aiohttp>=3.8.0",
    ],
    extras_require={
        "dev": [
            "pytest>=7.0",
            "pytest-asyncio>=0.21.0",
            "black>=22.0",
            "isort>=5.0",
            "mypy>=0.991",
        ],
    },
)
